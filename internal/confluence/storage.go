// Package confluence converts Confluence storage format to Markdown.
//
// Storage format is XHTML with Confluence's own elements mixed in: macros,
// page links, attachments. The HTML converts cleanly enough. The macros are the
// interesting part, because some of them are not content at all — a children or
// recently-updated macro is a query, and the navigation of a whole space can
// rest on them. A query cannot become Markdown, so it becomes a visible note
// saying what it was, rather than an empty gap where a page's contents used to
// appear.
package confluence

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Convert turns storage-format XHTML into Markdown.
func Convert(storage string) (string, error) {
	// Storage format is a fragment, and it uses namespace prefixes without
	// declaring them. Wrapping it declares them and gives the parser a root.
	wrapped := `<root xmlns:ac="ac" xmlns:ri="ri">` + storage + `</root>`

	c := &converter{decoder: xml.NewDecoder(strings.NewReader(wrapped))}
	c.decoder.Strict = false
	c.decoder.Entity = xml.HTMLEntity
	// Deliberately no AutoClose. Storage format is well-formed XHTML, and the
	// HTML void-element list contains "link", which would swallow every
	// <ac:link> — the element page-to-page links are made of.

	if err := c.run(); err != nil {
		return "", err
	}
	return tidy(c.out.String()), nil
}

type converter struct {
	decoder *xml.Decoder
	out     strings.Builder

	listDepth  int
	ordered    []bool
	itemIndex  []int
	inCode     bool
	tableCells []string
	tableRows  int
	inCell     bool
	cell       strings.Builder
	linkHref   string
	linkText   strings.Builder
	inLink     bool
}

func (c *converter) run() error {
	for {
		token, err := c.decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("confluence: %w", err)
		}

		switch t := token.(type) {
		case xml.StartElement:
			c.start(t)
		case xml.EndElement:
			c.end(t)
		case xml.CharData:
			c.text(string(t))
		}
	}
}

func (c *converter) write(s string) {
	if c.inLink {
		c.linkText.WriteString(s)
		return
	}
	if c.inCell {
		c.cell.WriteString(s)
		return
	}
	c.out.WriteString(s)
}

func (c *converter) text(s string) {
	if c.inCode {
		c.write(s)
		return
	}
	// Collapse the whitespace XHTML is indented with; Markdown treats a run of
	// spaces and a single space the same, but a newline mid-sentence is noise.
	collapsed := strings.Join(strings.Fields(s), " ")
	if collapsed == "" {
		if strings.ContainsAny(s, " \t\n") {
			c.write(" ")
		}
		return
	}
	if strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\n") {
		collapsed = " " + collapsed
	}
	if strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\n") {
		collapsed += " "
	}
	c.write(collapsed)
}

func (c *converter) start(e xml.StartElement) {
	switch strings.ToLower(e.Name.Local) {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(e.Name.Local[1] - '0')
		c.write("\n\n" + strings.Repeat("#", level) + " ")
	case "p":
		c.write("\n\n")
	case "br":
		c.write("  \n")
	case "hr":
		c.write("\n\n---\n\n")
	case "strong", "b":
		c.write("**")
	case "em", "i":
		c.write("*")
	case "del", "s", "strike":
		c.write("~~")
	case "code":
		c.write("`")
	case "pre":
		c.inCode = true
		c.write("\n\n```\n")
	case "ul", "ol":
		c.listDepth++
		c.ordered = append(c.ordered, strings.ToLower(e.Name.Local) == "ol")
		c.itemIndex = append(c.itemIndex, 0)
		if c.listDepth == 1 {
			c.write("\n\n")
		}
	case "li":
		depth := len(c.ordered) - 1
		if depth < 0 {
			return
		}
		c.itemIndex[depth]++
		indent := strings.Repeat("  ", depth)
		if c.ordered[depth] {
			c.write(fmt.Sprintf("\n%s%d. ", indent, c.itemIndex[depth]))
		} else {
			c.write("\n" + indent + "- ")
		}
	case "blockquote":
		c.write("\n\n> ")
	case "table":
		c.tableRows = 0
		c.write("\n\n")
	case "tr":
		c.tableCells = nil
	case "th", "td":
		c.inCell = true
		c.cell.Reset()
	case "a":
		c.inLink = true
		c.linkText.Reset()
		c.linkHref = attr(e, "href")
	case "structured-macro":
		c.macro(e)
	case "link":
		c.inLink = true
		c.linkText.Reset()
		c.linkHref = ""
	case "page":
		if title := attr(e, "content-title"); title != "" {
			c.inLink = false
			c.write("[[" + title + "]]")
		}
	case "attachment":
		if filename := attr(e, "filename"); filename != "" {
			c.write("[[" + filename + "]]")
		}
	case "image":
		c.write("!")
	}
}

func (c *converter) end(e xml.EndElement) {
	switch strings.ToLower(e.Name.Local) {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.write("\n\n")
	case "p":
		c.write("\n\n")
	case "strong", "b":
		c.write("**")
	case "em", "i":
		c.write("*")
	case "del", "s", "strike":
		c.write("~~")
	case "code":
		c.write("`")
	case "pre":
		c.write("\n```\n\n")
		c.inCode = false
	case "ul", "ol":
		c.listDepth--
		if len(c.ordered) > 0 {
			c.ordered = c.ordered[:len(c.ordered)-1]
			c.itemIndex = c.itemIndex[:len(c.itemIndex)-1]
		}
		if c.listDepth == 0 {
			c.write("\n\n")
		}
	case "blockquote":
		c.write("\n\n")
	case "th", "td":
		c.inCell = false
		text := strings.TrimSpace(c.cell.String())
		c.tableCells = append(c.tableCells, strings.ReplaceAll(text, "|", "\\|"))
	case "tr":
		if len(c.tableCells) == 0 {
			return
		}
		c.out.WriteString("| " + strings.Join(c.tableCells, " | ") + " |\n")
		c.tableRows++
		if c.tableRows == 1 {
			c.out.WriteString("|" + strings.Repeat(" --- |", len(c.tableCells)) + "\n")
		}
	case "table":
		c.write("\n")
	case "a", "link":
		c.inLink = false
		text := strings.TrimSpace(c.linkText.String())
		switch {
		case c.linkHref != "" && text != "":
			c.write("[" + text + "](" + c.linkHref + ")")
		case c.linkHref != "":
			c.write("<" + c.linkHref + ">")
		case text != "":
			c.write(text)
		}
	}
}

// queryMacros are macros that produce a list of pages at render time. They are
// navigation, not content, and a whole space's structure can hang off one.
var queryMacros = map[string]string{
	"children":         "a list of this page's children",
	"recently-updated": "recently updated pages",
	"pagetree":         "a page tree",
	"contentbylabel":   "pages with a given label",
	"toc":              "a table of contents",
	"include":          "another page, included",
	"excerpt-include":  "an excerpt of another page",
	"blog-posts":       "blog posts",
}

func (c *converter) macro(e xml.StartElement) {
	name := attr(e, "name")

	if described, isQuery := queryMacros[name]; isQuery {
		// Say what was here. An empty gap where a page's navigation used to be
		// is a thing nobody notices until they need it.
		c.write(fmt.Sprintf("\n\n> **Was a %s macro:** %s. It was a query, not text, "+
			"so it could not be carried across.\n\n", name, described))
		return
	}

	switch name {
	case "code", "noformat":
		c.inCode = true
		c.write("\n\n```\n")
	case "info", "note", "tip", "warning", "panel":
		c.write(fmt.Sprintf("\n\n> **%s**\n> ", strings.ToUpper(name)))
	case "status":
		c.write("`")
	case "":
		// not a macro after all
	default:
		c.write(fmt.Sprintf("\n\n<!-- unconverted macro: %s -->\n\n", name))
	}
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

// tidy collapses the blank lines the element handlers leave behind.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmed) == "" {
			blank++
			if blank > 1 || len(out) == 0 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		// Leading spaces are kept: they are list nesting, not stray indentation.
		out = append(out, trimmed)
	}

	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
