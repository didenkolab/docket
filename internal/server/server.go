// Package server serves a vault over HTTP: a board for people who do not run
// Obsidian, and an API.
//
// It is a second client to the same files, not an owner of them. It holds no
// state the files do not have, reads everything from disk on every request, and
// commits every write. Obsidian, an agent and this server can be pointed at one
// repository at the same time.
package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

//go:embed templates/*.html static/*
var assets embed.FS

// ErrStale means the file changed between the client reading it and writing it
// back.
var ErrStale = errors.New("the file changed since you loaded it")

// Options configure a server.
type Options struct {
	// Author is who writes are attributed to when nobody signs in.
	Author gitvcs.Author
	// Host is the git host that answers who someone is. Nil runs the server
	// unauthenticated, which it then says out loud rather than implying.
	Host access.Host
	// Recheck is how often to re-ask the host about a signed-in person, and so
	// how long a revocation takes to bite.
	Recheck time.Duration
	// SessionLife is how long a session lasts before it has to be renewed.
	SessionLife time.Duration
}

// Server serves one vault.
type Server struct {
	root   string
	repo   *gitvcs.Repo
	author gitvcs.Author
	tmpl   *template.Template
	auth   *authority

	// writes is held for the whole read-modify-write of a task, so two
	// requests cannot interleave. It says nothing about Obsidian or an agent
	// writing the same file — that is what the version check is for.
	writes sync.Mutex

	// now is injectable so tests can assert on timestamps.
	now func() time.Time
}

// New opens a vault for serving.
func New(root string, opts Options) (*Server, error) {
	if _, err := project.Load(root); err != nil {
		return nil, err
	}
	repo, err := gitvcs.Open(root)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"categoryClass": categoryClass,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		root:   root,
		repo:   repo,
		author: opts.Author,
		tmpl:   tmpl,
		now:    time.Now,
	}
	if opts.Host != nil {
		recheck, life := opts.Recheck, opts.SessionLife
		if recheck <= 0 {
			recheck = 5 * time.Minute
		}
		if life <= 0 {
			life = 12 * time.Hour
		}
		s.auth = newAuthority(opts.Host, recheck, life)
	}
	return s, nil
}

// loadConfigQuietly is for pages that must render even when the vault will not
// load — the sign-in page has to be reachable before anything else works.
func loadConfigQuietly(root string) (*project.Config, error) { return project.Load(root) }

// Handler routes every request the server answers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleBoard)
	mux.HandleFunc("GET /task/{key}", s.handleTask)
	mux.HandleFunc("POST /task/{key}/status", s.handleMove)
	mux.HandleFunc("POST /task/{key}/comment", s.handleComment)
	mux.HandleFunc("GET /task/{key}/edit", s.handleEditForm)
	mux.HandleFunc("POST /task/{key}/edit", s.handleEdit)
	mux.HandleFunc("POST /task/{key}/attach", s.handleAttach)
	mux.HandleFunc("POST /task/{key}/delete", s.handleDeleteTask)
	mux.HandleFunc("GET /file/{path...}", s.handleFile)
	mux.HandleFunc("GET /new", s.handleNewForm)
	mux.HandleFunc("POST /new", s.handleNew)
	mux.HandleFunc("GET /pages", s.handlePages)
	mux.HandleFunc("GET /pages/new", s.handlePageNewForm)
	mux.HandleFunc("POST /pages/save", s.handlePageSave)
	mux.HandleFunc("POST /preview", s.handlePreview)
	mux.HandleFunc("GET /page/{path...}", s.handlePage)
	mux.HandleFunc("GET /edit/page/{path...}", s.handlePageEditForm)
	mux.HandleFunc("POST /pages/delete", s.handlePageDelete)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSaveSettings)
	mux.HandleFunc("GET /admin", s.handleAdmin)
	mux.HandleFunc("GET /sign-in", s.handleSignInForm)
	mux.HandleFunc("POST /sign-in", s.handleSignIn)
	mux.HandleFunc("POST /sign-out", s.handleSignOut)

	mux.HandleFunc("GET /api/tasks", s.apiListTasks)
	mux.HandleFunc("POST /api/tasks", s.apiCreateTask)
	mux.HandleFunc("GET /api/tasks/{key}", s.apiGetTask)
	mux.HandleFunc("PATCH /api/tasks/{key}", s.apiPatchTask)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /static/", http.FileServerFS(assets))

	return s.guard(mux)
}

// version identifies the exact bytes a client saw, so a write can refuse to
// land on top of a change it never knew about.
func version(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

// keyOf is the task key from the URL.
func keyOf(r *http.Request) string { return r.PathValue("key") }

// locate finds a task's file. The key stopped being the path in ADR-0005, so
// this is a lookup — kept in one place so nothing else has to know.
func (s *Server) locate(key string) (rel, full string, err error) {
	c, err := project.Load(s.root)
	if err != nil {
		return "", "", err
	}
	rel, err = vault.Find(s.root, c, key)
	if err != nil {
		return "", "", err
	}
	return rel, filepath.Join(s.root, filepath.FromSlash(rel)), nil
}

func (s *Server) loadTask(key string) (*task.Task, string, error) {
	_, full, err := s.locate(key)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, "", err
	}
	t, err := task.Parse(raw)
	if err != nil {
		return nil, "", err
	}
	return t, version(raw), nil
}

// editTask performs a read-modify-write on one task and commits it.
//
// The expected version is checked against what is on disk at the moment of the
// write. When Obsidian or an agent has touched the file in the meantime the
// edit is refused rather than applied on top of content the caller never saw.
//
// mutate runs while the write lock is held, so it may touch other files as
// well — an uploaded attachment, the neighbours a reordering renumbered. It
// returns their paths, and they land in the same commit: half of a change is
// worse than none of it.
func (s *Server) editTask(
	key, expected string,
	author gitvcs.Author,
	mutate func(*task.Task) (message string, alsoCommit []string, err error),
) error {
	s.writes.Lock()
	defer s.writes.Unlock()

	rel, path, err := s.locate(key)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if expected != "" && version(raw) != expected {
		return ErrStale
	}

	t, err := task.Parse(raw)
	if err != nil {
		return err
	}
	message, companions, err := mutate(t)
	if err != nil {
		return err
	}
	if message == "" {
		return nil // nothing was asked for
	}
	t.Touch(s.now())
	if err := t.Sync(); err != nil {
		return err
	}

	content, err := t.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}

	// A title now lives in the file name, so changing it moves the file. Both
	// paths go into the commit, which is what makes git record a rename rather
	// than a deletion and an unrelated new file.
	paths := append([]string{rel}, companions...)
	if projectKey, _, err := project.SplitKey(t.Key); err == nil {
		if wanted := vault.PathFor(projectKey, t.Key, t.Title); wanted != rel {
			if err := vault.Rename(s.root, rel, wanted); err != nil {
				return err
			}
			paths = append(paths, wanted)
		}
	}

	return s.repo.Commit(paths, message, author)
}

func categoryClass(category string) string {
	switch category {
	case project.CategoryDone:
		return "done"
	case project.CategoryDoing:
		return "doing"
	default:
		return "todo"
	}
}
