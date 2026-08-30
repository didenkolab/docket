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

// Server serves one vault.
type Server struct {
	root   string
	repo   *gitvcs.Repo
	author gitvcs.Author
	tmpl   *template.Template

	// writes is held for the whole read-modify-write of a task, so two
	// requests cannot interleave. It says nothing about Obsidian or an agent
	// writing the same file — that is what the version check is for.
	writes sync.Mutex

	// now is injectable so tests can assert on timestamps.
	now func() time.Time
}

// New opens a vault for serving.
func New(root string, author gitvcs.Author) (*Server, error) {
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

	return &Server{
		root:   root,
		repo:   repo,
		author: author,
		tmpl:   tmpl,
		now:    time.Now,
	}, nil
}

// Handler routes every request the server answers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleBoard)
	mux.HandleFunc("GET /task/{key}", s.handleTask)
	mux.HandleFunc("POST /task/{key}/status", s.handleMove)
	mux.HandleFunc("POST /task/{key}/comment", s.handleComment)
	mux.HandleFunc("GET /new", s.handleNewForm)
	mux.HandleFunc("POST /new", s.handleNew)
	mux.HandleFunc("GET /pages", s.handlePages)
	mux.HandleFunc("GET /page/{path...}", s.handlePage)
	mux.HandleFunc("GET /search", s.handleSearch)

	mux.HandleFunc("GET /api/tasks", s.apiListTasks)
	mux.HandleFunc("POST /api/tasks", s.apiCreateTask)
	mux.HandleFunc("GET /api/tasks/{key}", s.apiGetTask)
	mux.HandleFunc("PATCH /api/tasks/{key}", s.apiPatchTask)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /static/", http.FileServerFS(assets))

	return mux
}

// version identifies the exact bytes a client saw, so a write can refuse to
// land on top of a change it never knew about.
func version(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

func (s *Server) taskPath(key string) string {
	return filepath.Join(s.root, vault.TasksDir, key+".md")
}

func (s *Server) loadTask(key string) (*task.Task, string, error) {
	raw, err := os.ReadFile(s.taskPath(key))
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
func (s *Server) editTask(
	key, expected string,
	author gitvcs.Author,
	mutate func(*task.Task) (message string, err error),
) error {
	s.writes.Lock()
	defer s.writes.Unlock()

	path := s.taskPath(key)
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
	message, err := mutate(t)
	if err != nil {
		return err
	}
	t.Touch(s.now())

	content, err := t.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}

	rel := filepath.ToSlash(filepath.Join(vault.TasksDir, key+".md"))
	return s.repo.Commit([]string{rel}, message, author)
}

// authorFor lets a caller act as themselves instead of as the server. An API
// client that cannot say who it is gets attributed to whoever started the
// server, which is honest — it is who is responsible for the write.
func (s *Server) authorFor(r *http.Request) gitvcs.Author {
	if header := r.Header.Get("X-Docket-Author"); header != "" {
		if a, err := gitvcs.ParseAuthor(header); err == nil {
			return a
		}
	}
	if form := r.FormValue("author"); form != "" {
		if a, err := gitvcs.ParseAuthor(form); err == nil {
			return a
		}
	}
	return s.author
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
