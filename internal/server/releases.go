package server

import (
	"net/http"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
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
		for _, path := range paths {
			t := versionOf(v.Repo, tag.Name, path)
			if t == nil {
				view.Other++
				continue
			}
			view.Tasks = append(view.Tasks, releaseTask{
				Key: t.Key, Title: t.Title,
				Status: t.Status, Category: t.StatusCategory,
				Added: since != "" && !exists(v.Repo, since, path),
			})
		}
		views = append(views, view)
	}
	return views, nil
}

// versionOf parses a file as a task at one point in history, or nil when it is
// not a task at all — a page, a board, the configuration.
func versionOf(repo *gitvcs.Repo, ref, path string) *task.Task {
	if !strings.HasSuffix(path, ".md") {
		return nil
	}
	raw, err := repo.At(ref, path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	t, err := task.Parse(raw)
	if err != nil || t.Key == "" {
		return nil
	}
	return t
}

func exists(repo *gitvcs.Repo, ref, path string) bool {
	raw, err := repo.At(ref, path)
	return err == nil && len(raw) > 0
}
