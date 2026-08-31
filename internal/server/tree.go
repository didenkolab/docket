package server

import (
	"sort"
	"strings"
)

// The knowledge base as a tree, because that is what it is.
//
// `docs/` has no schema on purpose — it is a wiki, and the folders somebody put
// pages in are the only structure it has. Showing it as a flat list of paths
// throws that away and gets worse the more there is: forty lines of
// `docs/runbooks/payments/reconciliation` with the first two segments repeated
// on every one of them.
//
// Folders are collapsible with <details> and nothing else. No script, no
// remembered state — a folder opens because it holds what you are looking for,
// and the page is small enough that reopening one costs nothing.

// pageNode is a folder or a page in the knowledge base.
type pageNode struct {
	// Name is what to show: the folder's name, or the note's name.
	Name string
	// Path is where the page is, from the space root, and is empty on a folder.
	Path string
	// Children are what is inside a folder, folders first.
	Children []pageNode
	// Pages is how many pages are anywhere beneath a folder, so a closed folder
	// says whether opening it is worth it.
	Pages int
}

// IsFolder reports whether this holds other things.
func (n pageNode) IsFolder() bool { return n.Path == "" }

// tree arranges paths said from the space root into folders and pages.
//
// The first segment of every path is `docs`, or the repository and then `docs`
// in a workspace, and neither is worth a level of its own: a page list that
// starts with one folder holding everything is a list with a wasted click. So
// the roots are what is inside them.
func tree(paths []string) []pageNode {
	root := &builder{children: map[string]*builder{}}
	for _, p := range paths {
		root.add(strings.Split(p, "/"), p)
	}

	// Unwrap the levels that hold nothing but one folder — `docs`, and in a
	// workspace the repository above it, which the heading already says.
	nodes := root.build()
	for len(nodes) == 1 && nodes[0].IsFolder() && nodes[0].Name == "docs" {
		nodes = nodes[0].Children
	}
	return nodes
}

// builder is the tree while it is being assembled.
type builder struct {
	children map[string]*builder
	// page is set on a leaf: the path to it.
	page string
}

func (b *builder) add(segments []string, full string) {
	if len(segments) == 0 {
		return
	}
	name := segments[0]
	if len(segments) == 1 {
		b.children[name] = &builder{children: map[string]*builder{}, page: full}
		return
	}
	child, ok := b.children[name]
	if !ok {
		child = &builder{children: map[string]*builder{}}
		b.children[name] = child
	}
	child.add(segments[1:], full)
}

// build turns the map into a sorted slice: folders first, then pages, each in
// name order. Folders first is what a file explorer does, and the structure is
// the thing somebody is scanning for.
func (b *builder) build() []pageNode {
	nodes := make([]pageNode, 0, len(b.children))
	for name, child := range b.children {
		node := pageNode{Name: name, Path: child.page}
		if child.page == "" {
			node.Children = child.build()
			node.Pages = count(node.Children)
		}
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsFolder() != nodes[j].IsFolder() {
			return nodes[i].IsFolder()
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes
}

// count is how many pages are anywhere beneath these nodes.
func count(nodes []pageNode) int {
	total := 0
	for _, n := range nodes {
		if n.IsFolder() {
			total += n.Pages
			continue
		}
		total++
	}
	return total
}

// pagesView is the knowledge base page.
type pagesView struct {
	Tree []pageNode
	// Total is how many pages there are, so the heading can say.
	Total int
	// Flat says the tree is one level deep, in which case the folders are not
	// worth drawing as folders.
	Flat bool
}

func newPagesView(paths []string) pagesView {
	nodes := tree(paths)
	view := pagesView{Tree: nodes, Total: len(paths), Flat: true}
	for _, n := range nodes {
		if n.IsFolder() {
			view.Flat = false
		}
	}
	return view
}
