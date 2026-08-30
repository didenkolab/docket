// Package adf converts Atlassian Document Format to Markdown.
//
// ADF is the JSON document format Jira Cloud stores rich text in. Every
// description, comment and text custom field arrives as a tree of typed nodes,
// and a vault stores Markdown, so this is the seam the whole import passes
// through.
//
// Nodes this package does not know are not dropped. They become a visible
// marker naming the type, because a silent hole in an imported description is
// worse than an ugly one: nobody goes looking for text they were never told
// went missing.
package adf

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Node is one ADF node. The shape is uniform enough that one struct covers the
// whole format.
type Node struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Content []Node          `json:"content,omitempty"`
	Marks   []Mark          `json:"marks,omitempty"`
	Attrs   map[string]any  `json:"attrs,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// Mark is inline formatting attached to a text node.
type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Convert turns an ADF document into Markdown.
func Convert(doc *Node) string {
	if doc == nil {
		return ""
	}
	var b strings.Builder
	writeBlocks(&b, doc.Content, "")
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// ConvertJSON turns raw ADF JSON into Markdown. Anything that is not a document
// comes back as the empty string rather than an error: an absent description is
// a normal thing, not a failure.
func ConvertJSON(raw []byte) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("adf: %w", err)
	}
	if doc.Type != "doc" {
		return "", nil
	}
	return Convert(&doc), nil
}

func writeBlocks(b *strings.Builder, nodes []Node, indent string) {
	for _, n := range nodes {
		writeBlock(b, n, indent)
	}
}

func writeBlock(b *strings.Builder, n Node, indent string) {
	switch n.Type {
	case "paragraph":
		if text := inline(n.Content); text != "" {
			fmt.Fprintf(b, "%s%s\n\n", indent, text)
		}

	case "heading":
		level := attrInt(n.Attrs, "level", 1)
		fmt.Fprintf(b, "%s%s %s\n\n", indent, strings.Repeat("#", level), inline(n.Content))

	case "bulletList":
		writeList(b, n, indent, func(int) string { return "- " })
		b.WriteString("\n")

	case "orderedList":
		start := attrInt(n.Attrs, "order", 1)
		writeList(b, n, indent, func(i int) string { return fmt.Sprintf("%d. ", start+i) })
		b.WriteString("\n")

	case "taskList":
		for _, item := range n.Content {
			box := "[ ]"
			if attrString(item.Attrs, "state") == "DONE" {
				box = "[x]"
			}
			fmt.Fprintf(b, "%s- %s %s\n", indent, box, inline(item.Content))
		}
		b.WriteString("\n")

	case "codeBlock":
		language := attrString(n.Attrs, "language")
		fmt.Fprintf(b, "%s```%s\n%s\n%s```\n\n", indent, language, plain(n.Content), indent)

	case "blockquote":
		var inner strings.Builder
		writeBlocks(&inner, n.Content, "")
		for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
			fmt.Fprintf(b, "%s> %s\n", indent, line)
		}
		b.WriteString("\n")

	case "panel":
		// A panel is a callout. Markdown has no such thing, so it becomes a
		// quote with its kind named, which keeps the emphasis visible.
		kind := attrString(n.Attrs, "panelType")
		var inner strings.Builder
		writeBlocks(&inner, n.Content, "")
		if kind != "" {
			fmt.Fprintf(b, "%s> **%s**\n", indent, strings.ToUpper(kind))
		}
		for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
			fmt.Fprintf(b, "%s> %s\n", indent, line)
		}
		b.WriteString("\n")

	case "rule":
		fmt.Fprintf(b, "%s---\n\n", indent)

	case "table":
		writeTable(b, n, indent)

	case "mediaSingle", "mediaGroup":
		for _, media := range n.Content {
			writeBlock(b, media, indent)
		}

	case "media":
		// The file itself is fetched separately; the reference is by id, which
		// apply resolves against the attachments it downloaded.
		id := attrString(n.Attrs, "id")
		alt := attrString(n.Attrs, "alt")
		if alt == "" {
			alt = "attachment"
		}
		fmt.Fprintf(b, "%s![%s](media:%s)\n\n", indent, alt, id)

	case "expand", "nestedExpand":
		title := attrString(n.Attrs, "title")
		fmt.Fprintf(b, "%s**%s**\n\n", indent, title)
		writeBlocks(b, n.Content, indent)

	case "":
		// nothing

	default:
		fmt.Fprintf(b, "%s<!-- unconverted %s -->\n\n", indent, n.Type)
		writeBlocks(b, n.Content, indent)
	}
}

func writeList(b *strings.Builder, list Node, indent string, marker func(int) string) {
	for i, item := range list.Content {
		prefix := marker(i)
		first := true

		for _, child := range item.Content {
			switch child.Type {
			case "paragraph":
				if first {
					fmt.Fprintf(b, "%s%s%s\n", indent, prefix, inline(child.Content))
					first = false
				} else {
					fmt.Fprintf(b, "%s%s%s\n", indent, strings.Repeat(" ", len(prefix)),
						inline(child.Content))
				}
			case "bulletList", "orderedList":
				writeList(b, child, indent+"  ", func(i int) string {
					if child.Type == "orderedList" {
						return fmt.Sprintf("%d. ", i+1)
					}
					return "- "
				})
			default:
				var inner strings.Builder
				writeBlock(&inner, child, indent+strings.Repeat(" ", len(prefix)))
				b.WriteString(inner.String())
			}
		}
	}
}

func writeTable(b *strings.Builder, table Node, indent string) {
	rows := table.Content
	if len(rows) == 0 {
		return
	}

	cellsOf := func(row Node) []string {
		var cells []string
		for _, cell := range row.Content {
			cells = append(cells, strings.ReplaceAll(inlineBlocks(cell.Content), "|", "\\|"))
		}
		return cells
	}

	header := cellsOf(rows[0])
	fmt.Fprintf(b, "%s| %s |\n", indent, strings.Join(header, " | "))
	fmt.Fprintf(b, "%s|%s\n", indent, strings.Repeat(" --- |", len(header)))

	for _, row := range rows[1:] {
		cells := cellsOf(row)
		for len(cells) < len(header) {
			cells = append(cells, "")
		}
		fmt.Fprintf(b, "%s| %s |\n", indent, strings.Join(cells, " | "))
	}
	b.WriteString("\n")
}

// inlineBlocks flattens the blocks inside a table cell onto one line, because
// a Markdown table cell cannot hold more than that.
func inlineBlocks(nodes []Node) string {
	var parts []string
	for _, n := range nodes {
		if text := inline(n.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func inline(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(applyMarks(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("  \n")
		case "mention":
			b.WriteString("@" + attrString(n.Attrs, "text"))
		case "emoji":
			if short := attrString(n.Attrs, "shortName"); short != "" {
				b.WriteString(short)
			} else {
				b.WriteString(attrString(n.Attrs, "text"))
			}
		case "inlineCard":
			url := attrString(n.Attrs, "url")
			b.WriteString("<" + url + ">")
		case "status":
			b.WriteString("`" + attrString(n.Attrs, "text") + "`")
		case "date":
			b.WriteString(attrString(n.Attrs, "timestamp"))
		default:
			b.WriteString(inline(n.Content))
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func applyMarks(text string, marks []Mark) string {
	if text == "" {
		return ""
	}
	// Code first: everything inside a code span is literal, so wrapping it in
	// emphasis afterwards would be wrong.
	for _, m := range marks {
		if m.Type == "code" {
			text = "`" + text + "`"
		}
	}
	for _, m := range marks {
		switch m.Type {
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "*" + text + "*"
		case "strike":
			text = "~~" + text + "~~"
		case "underline":
			// Markdown has no underline. Emphasis is the closest honest thing.
			text = "*" + text + "*"
		case "link":
			text = "[" + text + "](" + attrString(m.Attrs, "href") + ")"
		}
	}
	return text
}

func plain(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Text)
		b.WriteString(plain(n.Content))
	}
	return b.String()
}

func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	switch v := attrs[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return ""
	}
}

func attrInt(attrs map[string]any, key string, fallback int) int {
	if attrs == nil {
		return fallback
	}
	if v, ok := attrs[key].(float64); ok {
		return int(v)
	}
	return fallback
}
