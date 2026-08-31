package server

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
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
