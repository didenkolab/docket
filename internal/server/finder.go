package server

import (
	"net/url"
	"strings"
)

// The filter bar, as menus of links rather than a row of dropdowns.
//
// Seven native dropdowns side by side, every one of them saying "Any", read as
// a form to fill in rather than as filters — and a filter that is not set
// should not be taking up a control. So each dimension is a menu that shows
// what it is narrowed to, or its name in a quieter style when it is not.
//
// The options are links, not a form, and that is the whole trick. A link
// carries the entire state of the bar, so there are no hidden inputs shadowing
// each other and no script: the menu is a <details> the browser opens, and
// every option is a URL that is bookmarkable, back-buttonable and the same
// thing an agent would construct by hand. Clicking one does what clicking a
// link does.
//
// The one cost: words typed in the box and not yet submitted are lost when a
// filter is clicked, because a link does not carry them. Naming the filter you
// want and then typing is the ordinary order, and losing an unsubmitted word is
// cheaper than a script.

// finderMenu is one dimension of the filter bar.
type finderMenu struct {
	// Name is the dimension: "Status", "Label".
	Name string
	// Value is what it is narrowed to, or "" for any.
	Value string
	// Shown is Value as it should read — a tag gets its hash back.
	Shown string
	// Clear is where to go to drop this one filter.
	Clear   string
	Options []finderOption
}

// On reports whether this dimension is narrowing anything.
func (m finderMenu) On() bool { return m.Value != "" }

type finderOption struct {
	Label string
	Href  string
	On    bool
}

// menus builds the whole bar: one menu per dimension, each option a link that
// carries every other filter with it.
func (f filters) menus(v searchView) []finderMenu {
	anyone := func(vs []string) []string { return vs }

	return []finderMenu{
		f.menu("Project", f.Project, v.Projects, func(n *filters, s string) { n.Project = s }, "Any"),
		f.menu("Status", f.Status, v.StatusNames(),
			func(n *filters, s string) { n.Status = s }, "Any"),
		f.menu("Type", f.Type, v.Types, func(n *filters, s string) { n.Type = s }, "Any"),
		f.menu("Priority", f.Priority, v.Prios,
			func(n *filters, s string) { n.Priority = s }, "Any"),
		f.menu("Assignee", f.Assignee, append([]string{"!unassigned"}, anyone(v.Assignees)...),
			func(n *filters, s string) { n.Assignee = s }, "Anyone"),
		f.menu("Label", f.Label, v.Labels, func(n *filters, s string) { n.Label = s }, "Any"),
		f.menu("Tag", f.Tag, v.Tags, func(n *filters, s string) { n.Tag = s }, "Any"),
	}
}

// menu builds one dimension. set says which field an option changes, so the
// href carries the rest of the bar unchanged.
func (f filters) menu(name, current string, values []string,
	set func(*filters, string), anyLabel string) finderMenu {

	m := finderMenu{Name: name, Value: current, Shown: shownAs(name, current)}

	cleared := f
	set(&cleared, "")
	m.Clear = cleared.href()

	m.Options = append(m.Options, finderOption{Label: anyLabel, Href: m.Clear, On: current == ""})
	for _, value := range values {
		narrowed := f
		set(&narrowed, value)
		m.Options = append(m.Options, finderOption{
			Label: shownAs(name, value),
			Href:  narrowed.href(),
			On:    sameValue(name, value, current),
		})
	}
	return m
}

// shownAs is a value as it should read in the interface: a tag keeps its hash,
// because that is how a tag is written everywhere else, and nobody is a real
// value rather than a name.
func shownAs(dimension, value string) string {
	switch {
	case value == "":
		return ""
	case value == "!unassigned":
		return "Nobody"
	case dimension == "Tag":
		return "#" + value
	}
	return value
}

// sameValue compares a value with what is in force. Tags differ only in case,
// which is how Obsidian treats them.
func sameValue(dimension, value, current string) bool {
	if dimension == "Tag" {
		return strings.EqualFold(value, current)
	}
	return value == current
}

// href is this set of filters as a URL, with the empty ones left out so that a
// link is readable and a bookmark says what it does.
func (f filters) href() string {
	q := url.Values{}
	for name, value := range map[string]string{
		"q": f.Query, "project": f.Project, "status": f.Status, "type": f.Type,
		"priority": f.Priority, "assignee": f.Assignee, "label": f.Label, "tag": f.Tag,
	} {
		if value != "" {
			q.Set(name, value)
		}
	}
	if len(q) == 0 {
		return "/search"
	}
	return "/search?" + q.Encode()
}
