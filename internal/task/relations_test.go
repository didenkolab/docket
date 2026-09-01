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
