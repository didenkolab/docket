package server

import (
	"bytes"
	"html"
	"html/template"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
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

// buildIndex indexes one vault into ix, prefixing every path with where that
// vault sits in the space. A space of one has no prefix and nothing changes.
func buildIndex(ix *index, root, prefix string, c *project.Config) error {
	entries, err := vault.List(root, c)
	if err != nil {
		return err
	}
	// Indexed by note name, which is what Obsidian resolves — not by key, and
	// not by alias. A link this page renders as live and Obsidian renders as
	// dead would be worse than no rendering at all.
	for _, e := range entries {
		ix.put(e.Note(), "/task/"+e.Key)
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
			// Attachments are linked and embedded by their path, so they have
			// to be resolvable too — otherwise every screenshot renders as a
			// dead link.
			if rel, err := filepath.Rel(root, path); err == nil {
				rel = filepath.ToSlash(rel)
				if strings.HasPrefix(rel, vault.Attachments+"/") {
					href := "/file/" + join(prefix, rel)
					ix.put(rel, href)
					ix.put(filepath.Base(rel), href)
				}
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		withoutExt := strings.TrimSuffix(rel, ".md")
		if _, _, err := project.SplitKey(keyHead(filepath.Base(withoutExt))); err == nil {
			return nil // already indexed as a task
		}

		href := "/page/" + join(prefix, withoutExt)
		ix.put(withoutExt, href)
		ix.put(filepath.Base(withoutExt), href)
		return nil
	})
	return err
}

// keyHead is the first word of a note name, which is where a task's key sits.
func keyHead(name string) string {
	if space := strings.Index(name, " "); space >= 0 {
		return name[:space]
	}
	return name
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
			if display == target && strings.HasPrefix(href, "/file/") {
				// An embedded screenshot should not caption itself with a path.
				display = filepath.Base(target)
			}
			return "[" + display + "](" + escapeHref(href) + ")"
		}
		return `<span class="dead-link" title="resolves to nothing in this vault">` +
			html.EscapeString(display) + `</span>`
	})

	var out bytes.Buffer
	if err := markdown.Convert([]byte(rewriteCallouts(linked)), &out); err != nil {
		return template.HTML(template.HTMLEscapeString(body))
	}
	return template.HTML(out.String())
}

// escapeHref makes a path safe to put inside Markdown link parentheses.
//
// A note is named after its task and an attachment after the task it belongs
// to, so almost every one of these paths has a space in it — and a space inside
// `(...)` ends the URL, which turns the whole link into literal text with a
// stray bracket. Every segment is escaped; the slashes between them are not,
// because they are the path.
func escapeHref(href string) string {
	parts := strings.Split(href, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
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

// join puts a vault's prefix in front of a path inside it.
func join(prefix, rel string) string {
	if prefix == "" {
		return rel
	}
	return prefix + "/" + rel
}
