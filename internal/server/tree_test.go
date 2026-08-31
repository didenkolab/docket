package server

import "testing"

// The knowledge base is a free tree, so the page list has to be one. A flat
// list of paths repeats the first segments on every line and gets worse the
// more there is.
func TestPagesAreATree(t *testing.T) {
	view := newPagesView([]string{
		"docs/index",
		"docs/runbooks/payments/reconciliation",
		"docs/runbooks/oncall",
		"docs/labels/payments",
		"docs/labels/refunds",
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
	if got := deep.Children[0].Path; got != "docs/runbooks/payments/reconciliation" {
		t.Errorf("the leaf's path is %q, so the link would go nowhere", got)
	}
}

// A workspace says the repository first, and a vault with no folders at all
// should not be drawn as a tree of one.
func TestAFlatKnowledgeBaseIsNotDrawnAsATree(t *testing.T) {
	view := newPagesView([]string{"docs/index", "docs/pipeline"})
	if !view.Flat {
		t.Error("a knowledge base with no folders was called nested")
	}
	for _, n := range view.Tree {
		if n.IsFolder() {
			t.Errorf("%q is drawn as a folder", n.Name)
		}
	}
}
