package task

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const sample = `---
key: ACME-12
title: Fix login redirect loop
type: bug
status: In progress
status_category: doing
priority: high
assignee: agent/claude
parent: ACME-4
labels: [auth, regression]
created: 2026-08-30T10:12:00Z
updated: 2026-08-30T14:03:00Z
aliases: []
---

Body with a link to [[ACME-4]] and ` + "`[[not-a-link]]`" + ` in code.

## Comments
`

func parse(t *testing.T, src string) *Task {
	t.Helper()
	task, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return task
}

func TestParseReadsTheCoreProperties(t *testing.T) {
	task := parse(t, sample)

	for _, c := range []struct{ name, got, want string }{
		{"key", task.Key, "ACME-12"},
		{"title", task.Title, "Fix login redirect loop"},
		{"type", task.Type, "bug"},
		{"status", task.Status, "In progress"},
		{"status_category", task.StatusCategory, "doing"},
		{"priority", task.Priority, "high"},
		{"assignee", task.Assignee, "agent/claude"},
		{"parent", task.Parent, "ACME-4"},
		{"created", task.Created, "2026-08-30T10:12:00Z"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if len(task.Labels) != 2 || task.Labels[0] != "auth" {
		t.Errorf("labels = %v", task.Labels)
	}
}

func TestUntouchedTaskRoundTrips(t *testing.T) {
	task := parse(t, sample)
	got, err := task.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(got) != sample {
		t.Errorf("a round trip changed the file:\n--- got ---\n%s\n--- want ---\n%s", got, sample)
	}
}

func TestSetStatusMovesBothProperties(t *testing.T) {
	task := parse(t, sample)
	task.SetStatus("Done", "done")

	out, err := task.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "status: Done\n") {
		t.Errorf("status was not written:\n%s", text)
	}
	if !strings.Contains(text, "status_category: done\n") {
		t.Errorf("category was not written:\n%s", text)
	}
	if !strings.Contains(text, "title: Fix login redirect loop\n") {
		t.Error("an unrelated property was lost")
	}
	if !strings.Contains(text, "## Comments") {
		t.Error("the body was lost")
	}
}

func TestPropertyOrderSurvivesAnEdit(t *testing.T) {
	task := parse(t, sample)
	task.SetStatus("Done", "done")
	out, _ := task.Bytes()

	if got := strings.Index(string(out), "key:"); got > strings.Index(string(out), "title:") {
		t.Error("key moved after title")
	}
	if !strings.HasPrefix(string(out), "---\nkey: ACME-12\ntitle:") {
		t.Errorf("the frontmatter was reshuffled:\n%s", out)
	}
}

func TestTimestampsStayUnquoted(t *testing.T) {
	task := parse(t, sample)
	task.Touch(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC))

	out, _ := task.Bytes()
	if !strings.Contains(string(out), "updated: 2027-01-02T03:04:05Z\n") {
		t.Errorf("a quoted or reformatted timestamp:\n%s", out)
	}
}

func TestEmptyValueIsWrittenBare(t *testing.T) {
	task := parse(t, sample)
	task.Set("assignee", "")

	out, _ := task.Bytes()
	if !strings.Contains(string(out), "assignee:\n") {
		t.Errorf("an empty value was not written bare:\n%s", out)
	}
}

func TestValuesThatWouldChangeMeaningAreQuoted(t *testing.T) {
	for _, value := range []string{"123", "null", "true"} {
		task := parse(t, sample)
		task.Set("title", value)

		out, err := task.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		reparsed, err := Parse(out)
		if err != nil {
			t.Fatalf("value %q did not survive: %v\n%s", value, err, out)
		}
		if reparsed.Title != value {
			t.Errorf("title %q came back as %q", value, reparsed.Title)
		}
	}
}

func TestRemoveDropsAProperty(t *testing.T) {
	task := parse(t, sample)
	task.Remove("parent")

	out, _ := task.Bytes()
	if strings.Contains(string(out), "parent:") {
		t.Errorf("parent survived removal:\n%s", out)
	}
}

func TestSetListWritesFlowStyle(t *testing.T) {
	task := parse(t, sample)
	task.SetList("labels", []string{"one", "two"})

	out, _ := task.Bytes()
	if !strings.Contains(string(out), "labels: [one, two]\n") {
		t.Errorf("labels were not written in flow style:\n%s", out)
	}
}

func TestNestedPropertiesAreReported(t *testing.T) {
	nested := `---
key: ACME-1
fields:
  points: 3
labels: [a, b]
---
`
	task := parse(t, nested)
	got := task.NestedProperties()
	if len(got) != 1 || got[0] != "fields" {
		t.Errorf("NestedProperties() = %v, want [fields]", got)
	}
}

func TestLinksSkipCodeAndStripDecoration(t *testing.T) {
	body := "See [[ACME-4]] and [[docs/page|the page]] and [[ACME-5#Heading]].\n" +
		"Not `[[inline]]`.\n```\n[[fenced]]\n```\n"

	got := Links(body)
	want := []string{"ACME-4", "docs/page", "ACME-5"}
	if len(got) != len(want) {
		t.Fatalf("Links() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Links()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseRejectsFilesWithoutFrontmatter(t *testing.T) {
	for name, src := range map[string]string{
		"no delimiter":  "# Just a heading\n",
		"never closed":  "---\nkey: ACME-1\n",
		"not a mapping": "---\n- one\n- two\n---\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}

func TestErrNoFrontmatterIsMatchable(t *testing.T) {
	_, err := Parse([]byte("# Heading\n"))
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Errorf("err = %v, want ErrNoFrontmatter", err)
	}
}

func TestParseTime(t *testing.T) {
	if _, err := ParseTime("2026-08-30T10:12:00Z"); err != nil {
		t.Errorf("a valid timestamp was rejected: %v", err)
	}
	if _, err := ParseTime("30 August 2026"); err == nil {
		t.Error("an invalid timestamp was accepted")
	}
}
