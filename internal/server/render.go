package server

import (
	"bytes"
	"html"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// index maps everything a wikilink can point at to a URL in this server.
//
// It is rebuilt per request rather than cached. The whole point of the format
// is that Obsidian, an agent and this server all write the same files; a cache
// here would start serving a vault that no longer exists.
type index struct {
	targets map[string]string // lower-case target -> href
}

var skipDirs = map[string]bool{".git": true, ".obsidian": true, ".trash": true}

func buildIndex(root string, c *project.Config) (*index, error) {
	ix := &index{targets: map[string]string{}}

	entries, err := vault.List(root, c)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		ix.put(e.Key, "/task/"+e.Key)
		if e.Task != nil {
			for _, alias := range e.Task.Aliases {
				ix.put(alias, "/task/"+e.Key)
			}
		}
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, _, err := project.SplitKey(strings.TrimSuffix(rel, ".md")); err == nil {
			return nil // already indexed as a task
		}

		withoutExt := strings.TrimSuffix(rel, ".md")
		href := "/page/" + withoutExt
		ix.put(withoutExt, href)
		ix.put(filepath.Base(withoutExt), href)

		if raw, err := os.ReadFile(path); err == nil {
			if t, err := task.Parse(raw); err == nil {
				for _, alias := range t.Aliases {
					ix.put(alias, href)
				}
			}
		}
		return nil
	})
	return ix, err
}

func (ix *index) put(target, href string) {
	key := strings.ToLower(strings.TrimSpace(target))
	if key == "" {
		return
	}
	if _, taken := ix.targets[key]; !taken {
		ix.targets[key] = href
	}
}

func (ix *index) resolve(target string) (string, bool) {
	href, ok := ix.targets[strings.ToLower(strings.TrimSpace(target))]
	return href, ok
}

var (
	wikilink    = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
	fencedBlock = regexp.MustCompile("(?s)```.*?```")
	inlineCode  = regexp.MustCompile("`[^`\n]*`")

	// Raw HTML is rendered rather than escaped, because Obsidian renders it
	// too and a page that looks different in the two clients is a page nobody
	// trusts. The content is the team's own vault, the same trust boundary the
	// repository already draws.
	markdown = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()),
	)
)

// renderMarkdown turns a task or page body into HTML, resolving wikilinks
// against the vault. A link that resolves to nothing is shown as dead rather
// than as ordinary text — a broken link people cannot see is a broken link
// nobody fixes.
func renderMarkdown(body string, ix *index) template.HTML {
	linked := replaceOutsideCode(body, func(target string) string {
		display := target
		if bar := strings.Index(target, "|"); bar >= 0 {
			display, target = target[bar+1:], target[:bar]
		}
		if hash := strings.Index(target, "#"); hash >= 0 {
			target = target[:hash]
		}
		if href, ok := ix.resolve(target); ok {
			return "[" + display + "](" + href + ")"
		}
		return `<span class="dead-link" title="resolves to nothing in this vault">` +
			html.EscapeString(display) + `</span>`
	})

	var out bytes.Buffer
	if err := markdown.Convert([]byte(linked), &out); err != nil {
		return template.HTML(template.HTMLEscapeString(body))
	}
	return template.HTML(out.String())
}

// replaceOutsideCode rewrites wikilinks everywhere except inside code spans and
// fenced blocks, where they are examples rather than links.
func replaceOutsideCode(body string, rewrite func(target string) string) string {
	var out strings.Builder
	rest := body

	for len(rest) > 0 {
		code := nextCode(rest)
		if code == nil {
			out.WriteString(rewriteLinks(rest, rewrite))
			break
		}
		out.WriteString(rewriteLinks(rest[:code[0]], rewrite))
		out.WriteString(rest[code[0]:code[1]])
		rest = rest[code[1]:]
	}
	return out.String()
}

// nextCode finds the first code span or fenced block, whichever comes first.
func nextCode(s string) []int {
	fence := fencedBlock.FindStringIndex(s)
	span := inlineCode.FindStringIndex(s)

	switch {
	case fence == nil:
		return span
	case span == nil:
		return fence
	case fence[0] <= span[0]:
		return fence
	default:
		return span
	}
}

func rewriteLinks(s string, rewrite func(target string) string) string {
	return wikilink.ReplaceAllStringFunc(s, func(m string) string {
		return rewrite(strings.TrimSuffix(strings.TrimPrefix(m, "[["), "]]"))
	})
}
