package project

import (
	"strings"
	"testing"
)

// Every relation has an inverse except the symmetric one, and the words for
// each direction have to be there for a page to read as a sentence.
func TestEveryRelationSaysBothDirections(t *testing.T) {
	c := &Config{
		Name: "x", Projects: []Project{{Key: "ACME"}},
		Declared: []Relation{
			{Name: "tests", Inverse: "tested_by"},
			{Name: "threatens", Inverse: "threatened_by", Says: "threatens", Said: "is threatened by"},
			{Name: "informs"},
		},
	}

	byName := map[string]Relation{}
	for _, r := range c.Relations() {
		byName[r.Name] = r
	}
	for _, r := range c.Relations() {
		if r.Says == "" || r.Said == "" {
			t.Errorf("%s does not say how it reads", r.Name)
		}
		if r.Inverse == "" {
			if r.Says != r.Said {
				t.Errorf("%s has no inverse, so it must be symmetric", r.Name)
			}
			continue
		}
		back, ok := byName[r.Inverse]
		if !ok {
			t.Errorf("%s names an inverse %q that does not exist", r.Name, r.Inverse)
			continue
		}
		if back.Inverse != r.Name {
			t.Errorf("%s and %s do not point at each other", r.Name, r.Inverse)
		}
	}
}

// A vault declares the pair once and gets both directions, with words nobody
// had to write: `tested_by` reads as "tested by".
func TestADeclaredRelationBecomesBothDirections(t *testing.T) {
	c := &Config{Declared: []Relation{{Name: "tests", Inverse: "tested_by"}}}

	forward, ok := c.RelationOf("tests")
	if !ok {
		t.Fatal("the declared relation is not there")
	}
	back, ok := c.RelationOf("tested_by")
	if !ok {
		t.Fatal("its inverse was not made")
	}
	if forward.Says != "tests" || forward.Said != "tested by" {
		t.Errorf("forward reads %q / %q", forward.Says, forward.Said)
	}
	if back.Says != "tested by" || back.Said != "tests" {
		t.Errorf("back reads %q / %q", back.Says, back.Said)
	}

	// And the ones the format ships are still there: adding a verb is not
	// replacing the vocabulary.
	if !c.IsRelation("blocks") || !c.IsRelation("relates") {
		t.Error("declaring a relation dropped the built-in ones")
	}
}

func TestOnlyKnownRelationsAreRelations(t *testing.T) {
	c := &Config{}
	if !c.IsRelation("blocks") || !c.IsRelation("relates") {
		t.Error("a shipped relation is not recognised")
	}
	if c.IsRelation("parent") {
		t.Error("parent is hierarchy, not a relation — the distinction Jira warns about")
	}
	if c.IsRelation("nonsense") {
		t.Error("anything is a relation")
	}
}

// What a vault may not declare. Each of these would make two vaults mean
// different things by the same word, or make one property two things at once.
func TestARelationCannotBeDeclaredOverSomethingElse(t *testing.T) {
	for _, c := range []struct {
		what     string
		config   Config
		mentions string
	}{
		{"a built-in verb", Config{Declared: []Relation{{Name: "blocks", Inverse: "x"}}}, "already has"},
		{"a property the format owns",
			Config{Declared: []Relation{{Name: "status"}}}, "owns"},
		{"a declared field",
			Config{
				Fields:   []Field{{Name: "risk", Kind: "text"}},
				Declared: []Relation{{Name: "risk"}},
			}, "also declared as a field"},
		{"the same verb twice",
			Config{Declared: []Relation{{Name: "tests", Inverse: "tested_by"}, {Name: "tests"}}},
			"declared twice"},
		{"its own inverse",
			Config{Declared: []Relation{{Name: "mirrors", Inverse: "mirrors"}}}, "its own inverse"},
		{"a name YAML cannot hold plainly",
			Config{Declared: []Relation{{Name: "is tested by"}}}, "property name"},
	} {
		err := c.config.validateRelations()
		if err == nil {
			t.Errorf("%s was accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.mentions) {
			t.Errorf("%s: refused with %q, which does not say %q", c.what, err, c.mentions)
		}
	}
}
