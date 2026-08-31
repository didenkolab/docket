package server

import "testing"

// The knowledge base is a free tree, so the page list has to be one. A flat
// list of paths repeats the first segments on every line and gets worse the
// more there is.
func TestPagesAreATree(t *testing.T) {
	view := newPagesView([]titled{
		{Path: "docs/index"},
		{Path: "docs/runbooks/payments/reconciliation", Title: "Daily reconciliation"},
		{Path: "docs/runbooks/oncall"},
		{Path: "docs/labels/payments"},
		{Path: "docs/labels/refunds"},
	})

	if view.Total != 5 {
		t.Errorf("Total = %d", view.Total)
	}
	if view.Flat {
		t.Error("a tree with folders in it was called flat")
	}

	// docs holds everything, so it is not worth a level of its own.
	names := make([]string, 0, len(view.Tree))
	for _, n := range view.Tree {
		names = append(names, n.Name)
	}
	// Folders first, then pages, each in name order.
	want := []string{"labels", "runbooks", "index"}
	if len(names) != len(want) {
		t.Fatalf("roots = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("roots = %v, want %v", names, want)
			break
		}
	}

	labels := view.Tree[0]
	if !labels.IsFolder() || labels.Pages != 2 {
		t.Errorf("labels: folder = %v, pages = %d", labels.IsFolder(), labels.Pages)
	}

	// A folder counts what is anywhere beneath it, not just its own children.
	runbooks := view.Tree[1]
	if runbooks.Pages != 2 {
		t.Errorf("runbooks holds %d page(s), want the nested one counted too", runbooks.Pages)
	}
	deep := runbooks.Children[0]
	if deep.Name != "payments" || len(deep.Children) != 1 {
		t.Fatalf("the nested folder is %+v", deep)
	}
	leaf := deep.Children[0]
	if leaf.Path != "docs/runbooks/payments/reconciliation" {
		t.Errorf("the leaf's path is %q, so the link would go nowhere", leaf.Path)
	}
	// A page is shown by the title it gives itself, and keeps its file name
	// beside it — that is what a wikilink to it has to say.
	if leaf.Name != "Daily reconciliation" {
		t.Errorf("the leaf reads %q, want its title", leaf.Name)
	}
	if leaf.Note != "reconciliation" {
		t.Errorf("the file name is %q, so nobody could write a link to it", leaf.Note)
	}

	// A page whose title is its file name says it once.
	for _, n := range view.Tree {
		if n.Name == "index" && n.Note != "" {
			t.Errorf("index repeats its own name as %q", n.Note)
		}
	}
}

// A workspace says the repository first, and a vault with no folders at all
// should not be drawn as a tree of one.
func TestAFlatKnowledgeBaseIsNotDrawnAsATree(t *testing.T) {
	view := newPagesView([]titled{{Path: "docs/index"}, {Path: "docs/pipeline"}})
	if !view.Flat {
		t.Error("a knowledge base with no folders was called nested")
	}
	for _, n := range view.Tree {
		if n.IsFolder() {
			t.Errorf("%q is drawn as a folder", n.Name)
		}
	}
}

// Numbering a file is a statement about order; a title is not. Sorting by the
// title would scramble a sequence somebody built by hand — which is what the
// decisions folder is.
func TestNumberedPagesKeepTheirOrder(t *testing.T) {
	view := newPagesView([]titled{
		{Path: "docs/decisions/0002-go-and-a-single-binary", Title: "Go, and a single binary"},
		{Path: "docs/decisions/0001-vault-as-source-of-truth", Title: "The vault is the source of truth"},
		{Path: "docs/decisions/0003-a-vault-holds-several-projects", Title: "A vault holds several projects"},
	})

	decisions := view.Tree[0]
	if decisions.Name != "decisions" {
		t.Fatalf("the folder is %q", decisions.Name)
	}
	want := []string{
		"The vault is the source of truth",
		"Go, and a single binary",
		"A vault holds several projects",
	}
	for i, title := range want {
		if got := decisions.Children[i].Name; got != title {
			t.Errorf("position %d reads %q, want %q — the file numbers say the order",
				i+1, got, title)
		}
	}
}
