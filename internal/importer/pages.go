package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vadymdidenkolab/docket/internal/confluence"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// sourcePage is the part of a Confluence page an import uses.
type sourcePage struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	ParentID string `json:"parentId"`
	Version  struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
}

// writePages turns a space into a tree of Markdown pages under docs/.
//
// The tree follows the source's parent links, so a space that people navigated
// by hierarchy still reads as one. Titles become file names because that is
// what a wikilink to a page will say.
func writePages(snap Reader, root, space string) (int, error) {
	var pages []sourcePage
	err := snap.Each("pages/"+space+".jsonl", func(raw json.RawMessage) error {
		var page sourcePage
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		if page.Status != "" && page.Status != "current" {
			return nil // drafts and trash are not the space
		}
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		return 0, err
	}

	byID := map[string]sourcePage{}
	for _, page := range pages {
		byID[page.ID] = page
	}

	written := 0
	taken := map[string]bool{}

	for _, page := range pages {
		body, err := confluence.Convert(page.Body.Storage.Value)
		if err != nil {
			return written, fmt.Errorf("page %q: %w", page.Title, err)
		}

		rel := pagePath(page, byID, space, taken)
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return written, err
		}

		front := fmt.Sprintf("---\ntitle: %s\ntype: page\nx_source_id: %q\nx_source_version: %d\n---\n\n",
			yamlValue(page.Title), page.ID, page.Version.Number)
		if err := os.WriteFile(full, []byte(front+body), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// pagePath places a page under docs/<space>/<ancestors…>/<title>.md, falling
// back to a numbered name when two siblings share a title.
func pagePath(page sourcePage, byID map[string]sourcePage, space string, taken map[string]bool) string {
	var parts []string
	for cursor := page.ParentID; cursor != ""; {
		parent, ok := byID[cursor]
		if !ok {
			break
		}
		parts = append([]string{fileSlug(parent.Title)}, parts...)
		cursor = parent.ParentID
		if len(parts) > 12 {
			break // a cycle in the parent links, which the source should not have
		}
	}

	dir := strings.Join(append([]string{vault.DocsDir, fileSlug(space)}, parts...), "/")
	base := fileSlug(page.Title)
	candidate := dir + "/" + base + ".md"

	for n := 2; taken[strings.ToLower(candidate)]; n++ {
		candidate = fmt.Sprintf("%s/%s-%d.md", dir, base, n)
	}
	taken[strings.ToLower(candidate)] = true
	return candidate
}

// fileSlug keeps a title readable while making it safe as a file name — a
// wikilink to a page is written with its title, so mangling it would break
// every link at once.
func fileSlug(title string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", " -", "*", "", "?", "", "\"", "'",
		"<", "(", ">", ")", "|", "-", "#", "", "^", "", "[", "(", "]", ")",
	)
	cleaned := strings.TrimSpace(replacer.Replace(title))
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	if cleaned == "" {
		return "untitled"
	}
	// A hundred and twenty bytes, cut at a character boundary.
	//
	// It was cut at the byte, which is the same thing in English and is not in
	// anything else: a Cyrillic letter is two bytes, so the cut landed inside
	// one and produced a name the file system refuses outright — "illegal byte
	// sequence", on a real space whose pages are titled in Russian.
	//
	// Bytes rather than characters is the limit that matters, because that is
	// what a file system counts, and 255 is the usual ceiling. A hundred and
	// twenty leaves room for the folders a Confluence tree nests it in.
	if len(cleaned) > 120 {
		cut := 120
		for cut > 0 && !utf8.RuneStart(cleaned[cut]) {
			cut--
		}
		cleaned = strings.TrimSpace(cleaned[:cut])
	}
	if cleaned == "" {
		return "untitled"
	}
	return cleaned
}

func yamlValue(s string) string {
	if strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`") || strings.TrimSpace(s) != s {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
