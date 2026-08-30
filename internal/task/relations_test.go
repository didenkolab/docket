package task

import (
	"slices"
	"strings"
	"testing"
)

const bare = "---\nkey: ACME-1\ntitle: T\n---\n\nBody.\n"

func TestARelationIsWrittenAsLinksAndReadAsKeys(t *testing.T) {
	parsed, err := Parse([]byte(bare))
	if err != nil {
		t.Fatal(err)
	}
	parsed.SetRelated("blocked_by", []string{"ACME-4 Session model", "BETA-7 Ship the widget"})

	out, err := parsed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `blocked_by: ["[[ACME-4 Session model]]", "[[BETA-7 Ship the widget]]"]`) {
		t.Errorf("written as:\n%s", out)
	}

	back, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Related("blocked_by"); !slices.Equal(got, []string{"ACME-4", "BETA-7"}) {
		t.Errorf("read back as %v", got)
	}
}

// A vault written before relations existed, or by somebody typing a key, still
// has to be read.
func TestARelationReadsABareKeyToo(t *testing.T) {
	parsed, err := Parse([]byte("---\nkey: ACME-1\nblocks: [ACME-4]\n---\n\nBody.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Related("blocks"); !slices.Equal(got, []string{"ACME-4"}) {
		t.Errorf("got %v", got)
	}
	if raw := parsed.RawRelated("blocks"); !slices.Equal(raw, []string{"ACME-4"}) {
		t.Errorf("raw is %v, which is what check reports on", raw)
	}
}

func TestClearingARelationRemovesTheProperty(t *testing.T) {
	parsed, _ := Parse([]byte(bare))
	parsed.SetRelated("relates", []string{"ACME-4 Something"})
	parsed.SetRelated("relates", nil)

	out, _ := parsed.Bytes()
	if strings.Contains(string(out), "relates") {
		t.Errorf("an empty relation was left behind:\n%s", out)
	}
}

// Every relation has an inverse except the symmetric one, and the words for
// each direction have to be there for a page to read as a sentence.
func TestEveryRelationSaysBothDirections(t *testing.T) {
	for _, r := range Relations {
		if r.Says == "" || r.Said == "" {
			t.Errorf("%s does not say how it reads", r.Field)
		}
		if r.Inverse == "" {
			if r.Says != r.Said {
				t.Errorf("%s has no inverse, so it must be symmetric", r.Field)
			}
			continue
		}
		back, ok := RelationOf(r.Inverse)
		if !ok {
			t.Errorf("%s names an inverse %q that does not exist", r.Field, r.Inverse)
			continue
		}
		if back.Inverse != r.Field {
			t.Errorf("%s and %s do not point at each other", r.Field, r.Inverse)
		}
	}
}

func TestOnlyKnownRelationsAreRelations(t *testing.T) {
	if !IsRelation("blocks") || !IsRelation("relates") {
		t.Error("a shipped relation is not recognised")
	}
	if IsRelation("parent") {
		t.Error("parent is hierarchy, not a relation — the distinction Jira warns about")
	}
	if IsRelation("nonsense") {
		t.Error("anything is a relation")
	}
}
