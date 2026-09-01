package server

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A release page that lists every task and says nothing about the list is a
// page that has to be counted to be understood. The line is the point of it,
// and the fact it carries that a list of chips hides is how much of the work
// was actually finished when the tag was cut.
func TestDescribeRelease(t *testing.T) {
	for _, c := range []struct {
		what  string
		given releaseView
		want  string
	}{
		{
			what: "a release of finished work",
			given: releaseView{Tasks: []releaseTask{
				{Category: project.CategoryDone},
				{Category: project.CategoryDone},
			}},
			want: "2 tasks · 2 finished by the tag",
		},
		{
			what: "one whose work is mostly still open",
			given: releaseView{
				Tasks: []releaseTask{
					{Category: project.CategoryDone},
					{Category: project.CategoryDoing},
					{Category: project.CategoryDoing, Added: true},
					{Category: project.CategoryTodo},
				},
				Other: 3,
			},
			want: "4 tasks · 1 of them new · 1 finished by the tag · 2 still in flight · 3 other files",
		},
		{
			what:  "one task, said as one task",
			given: releaseView{Tasks: []releaseTask{{Category: project.CategoryDone, Added: true}}, Other: 1},
			want:  "1 task · 1 of them new · 1 finished by the tag · 1 other file",
		},
		{
			what:  "a tag that changed no task",
			given: releaseView{Other: 2},
			want:  "",
		},
	} {
		got := strings.Join(describeRelease(c.given), " · ")
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.what, got, c.want)
		}
	}
}

// A release page reads each tag's tree once, not once per file. On a real board
// a release shipping two and a half thousand files meant five thousand git
// processes and forty five seconds — for a page whose whole promise is that it
// is read out of the tag every time it is opened. What it says must not change.
func TestAReleaseReadsEachTagOnce(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gitvcs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	who := gitvcs.Author{Name: "Dana", Email: "dana@example.com"}
	tag := func(name string) {
		t.Helper()
		cmd := exec.Command("git", "tag", "-a", name, "-m", name)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git tag %s: %v: %s", name, err, out)
		}
	}

	firstPath, _, err := vault.Create(root, c, vault.NewOptions{Title: "In the first release", Now: noon})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit([]string{"."}, "the first", who); err != nil {
		t.Fatal(err)
	}
	tag("v1.0.0")

	laterPath, _, err := vault.Create(root, c, vault.NewOptions{Title: "Added after the first tag", Now: noon})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit([]string{"."}, "the second", who); err != nil {
		t.Fatal(err)
	}
	tag("v1.1.0")

	first := keyOfPath(firstPath)
	later := keyOfPath(laterPath)

	body := get(t, h, "/releases").Body.String()
	for _, want := range []string{"v1.0.0", "v1.1.0", later, "Added after the first tag"} {
		if !strings.Contains(body, want) {
			t.Errorf("the releases page does not mention %q", want)
		}
	}
	// The task that was already in v1.0.0 is not new in v1.1.0, and the one
	// that was not there is. That is the comparison the per-file read was for.
	if strings.Count(body, first) > 1 {
		t.Errorf("%s is in both releases; it shipped once", first)
	}
}

// keyOfPath is the task key in a path a vault write returned.
func keyOfPath(at string) string {
	name := at[strings.LastIndex(at, "/")+1:]
	if i := strings.Index(name, " "); i > 0 {
		return name[:i]
	}
	return name
}
