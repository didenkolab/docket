package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// A release is a git tag, and that is the whole of it.
//
// Jira keeps a version object with a name, dates and a released flag, puts a
// `fixVersion` on every issue pointing at it, and generates release notes by
// asking which issues carry that value. Three records of one fact, kept in step
// by hand.
//
// A tag is the release. Its date is its date, it exists or it does not, and
// what went into it is `git log v1.1.0..v1.2.0` — a question git answers
// without anybody maintaining the answer. The notes below are not written
// anywhere: they are read out of the repository each time the page is opened,
// which means they cannot be out of date.

type releasesView struct {
	Repos []repoReleases
	None  bool
}

// repoReleases is one repository's tags. A workspace has several repositories
// and therefore several release histories, because each project releases on its
// own schedule — which is what makes them separate repositories.
type repoReleases struct {
	Project  string
	Releases []releaseView
}

type releaseView struct {
	Name       string
	When       string
	Short      string
	Annotation string
	// Since is the release this one follows, so the page can say what the span
	// actually is rather than implying it.
	Since string
	Tasks []releaseTask
	// Other is how many files changed that were not tasks — pages, boards,
	// configuration. Counted rather than listed: it says the release contained
	// more than the list shows, which is honest, without filling the page.
	Other int
	// Says is the release in a line: how much work, how much of it new, how
	// much of it finished by the time the tag was cut.
	//
	// A release page without it is a list of forty rows that has to be counted
	// to be understood, which is the same as not being understood.
	Says []string
	// Open says this release is the one shown expanded. Only the newest is: a
	// page of every release fully spelled out is a page nobody reaches the
	// bottom of, and the newest is the one being asked about.
	Open bool
	// Board is the board as it stood at the tag, which is a thing git can
	// answer exactly.
	Board string
}

type releaseTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	// Added is set when the task did not exist before this release.
	Added bool
}

func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	view := releasesView{}
	for _, v := range s.sp().Vaults() {
		releases, err := s.releasesIn(v)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "Cannot read the tags", err.Error())
			return
		}
		if len(releases) == 0 {
			continue
		}
		view.Repos = append(view.Repos, repoReleases{
			Project: s.nameOf(v), Releases: releases,
		})
	}
	view.None = len(view.Repos) == 0

	s.render(w, r, "releases.html", c, "Releases", view)
}

// nameOf is what to call one repository on a page that shows several.
func (s *Server) nameOf(v *space.Vault) string {
	c, err := project.Load(v.Root)
	if err == nil {
		if keys := c.ProjectKeys(); len(keys) > 0 {
			return strings.Join(keys, ", ")
		}
	}
	if v.Prefix != "" {
		return v.Prefix
	}
	return ""
}

// releasesIn reads one repository's tags and works out what each contains.
func (s *Server) releasesIn(v *space.Vault) ([]releaseView, error) {
	tags, err := v.Repo.Releases()
	if err != nil {
		return nil, err
	}

	// One read per tag, not one per file. A release that ships two and a half
	// thousand files was five thousand git processes — forty five seconds for a
	// page whose whole promise is that it cannot drift, which nobody waits for.
	// Memoised because each tag is read twice: once as itself, once as the
	// thing the release before it is measured against.
	trees := map[string]map[string][]byte{}
	treeAt := func(ref string) map[string][]byte {
		if known, ok := trees[ref]; ok {
			return known
		}
		files, err := v.Repo.Files(ref, "")
		if err != nil {
			files = nil
		}
		trees[ref] = files
		return files
	}

	views := make([]releaseView, 0, len(tags))
	for i, tag := range tags {
		// The tags are newest first, so the one this release follows is the
		// next in the list. The oldest release follows nothing and contains
		// everything before it.
		since := ""
		if i+1 < len(tags) {
			since = tags[i+1].Name
		}

		view := releaseView{
			Name: tag.Name, Short: short(tag.Hash), Annotation: tag.Annotation,
			When: tag.When.UTC().Format("2006-01-02"), Since: since,
		}

		paths, err := v.Repo.Shipped(since, tag.Name)
		if err != nil {
			return nil, err
		}
		shipped := treeAt(tag.Name)
		var before map[string][]byte
		if since != "" {
			before = treeAt(since)
		}
		for _, path := range paths {
			t := versionOf(shipped[path], path)
			if t == nil {
				view.Other++
				continue
			}
			view.Tasks = append(view.Tasks, releaseTask{
				Key: t.Key, Title: t.Title,
				Status: t.Status, Category: t.StatusCategory,
				Added: since != "" && len(before[path]) == 0,
			})
		}
		view.Says = describeRelease(view)
		view.Board = "/branch/" + url.PathEscape(tag.Name)
		view.Open = i == 0
		views = append(views, view)
	}
	return views, nil
}

// describeRelease is the release in a line.
//
// Three facts, and only the ones that are true of this release: how much work,
// how much of it had never been in a release before, and how much of it was
// actually finished when the tag was cut. The last is the one a list of rows
// hides — a release whose work is half in flight looks exactly like one whose
// work is done, until somebody counts the chips.
func describeRelease(v releaseView) []string {
	if len(v.Tasks) == 0 {
		return nil
	}

	added, done, doing := 0, 0, 0
	for _, t := range v.Tasks {
		if t.Added {
			added++
		}
		switch t.Category {
		case project.CategoryDone:
			done++
		case project.CategoryDoing:
			doing++
		}
	}

	says := []string{plural(len(v.Tasks), "task", "tasks")}
	if added > 0 {
		says = append(says, fmt.Sprintf("%d of them new", added))
	}
	if done > 0 {
		says = append(says, fmt.Sprintf("%d finished by the tag", done))
	}
	if doing > 0 {
		says = append(says, fmt.Sprintf("%d still in flight", doing))
	}
	if v.Other > 0 {
		says = append(says, plural(v.Other, "other file", "other files"))
	}
	return says
}

// versionOf reads a file as it stood at one point in history, or nil when it is
// not a task at all — a page, a board, the configuration.
func versionOf(raw []byte, path string) *task.Task {
	if !strings.HasSuffix(path, ".md") || len(raw) == 0 {
		return nil
	}
	t, err := task.Parse(raw)
	if err != nil || t.Key == "" {
		return nil
	}
	return t
}
