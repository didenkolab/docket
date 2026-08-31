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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
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
	// BehindProxy says a reverse proxy sits in front, so the client to hold to
	// a rate limit is the one it names rather than the proxy itself. Off by
	// default: believing the header unasked lets anybody be somebody else.
	BehindProxy bool
	// DeviceClientID identifies this application to the host, so it can hand
	// over a token without anybody pasting one. It is public — there is no
	// secret in this flow — and empty means the sign-in page offers the token
	// field alone. See access/device.go.
	DeviceClientID string
	// OnLoopback says this server is reachable only from the machine it runs
	// on, so the credentials already on that machine may be used to sign in.
	//
	// It is not inferred from the address, because --behind-proxy binds
	// loopback and is reachable from the world: the two facts have to be
	// combined by whoever knows both. See local.go.
	OnLoopback bool
	// Unauthenticated runs the server with nobody signed in, whatever the
	// repositories say about their hosts.
	//
	// It has to be said rather than inferred from Host being nil: the hosts are
	// resolved per repository from their own remotes now, so a vault with a
	// remote would otherwise start asking people to sign in even when whoever
	// started it said not to. This is --auth none.
	Unauthenticated bool
}

// Server serves one space: a vault, or a workspace of them.
type Server struct {
	space  *space.Space
	author gitvcs.Author
	tmpl   *template.Template
	auth   *authority

	// writes is held for the whole read-modify-write of a task, so two
	// requests cannot interleave. It says nothing about Obsidian or an agent
	// writing the same file — that is what the version check is for.
	writes sync.Mutex

	// How fast one client may ask. Reading is cheap but not free — every board
	// parses every task — and changing is a git commit.
	reads       *limiter
	changes     *limiter
	signIns     *limiter
	behindProxy bool

	// deviceClientID is what this application calls itself when asking the
	// host for a sign-in code. Empty turns the button off.
	deviceClientID string
	// recheck is how often a host is re-asked about somebody, and so how long a
	// revocation takes to bite. The Access page says it out loud.
	recheck time.Duration
	// onLoopback says the machine's own credentials may sign somebody in.
	onLoopback bool

	// now is injectable so tests can assert on timestamps.
	now func() time.Time
}

// New opens a vault, or a workspace of them, for serving.
func New(root string, opts Options) (*Server, error) {
	sp, err := space.Open(root)
	if err != nil {
		return nil, err
	}
	if _, err := sp.Config(); err != nil {
		return nil, err
	}
	// Every write is a commit, so refuse a space that cannot make one.
	if err := sp.RequireGit(); err != nil {
		return nil, err
	}

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"categoryClass": categoryClass,
		// Two tags are the same tag when they differ only in case, which is
		// what Obsidian does.
		"sameTag": func(a, b string) bool { return strings.EqualFold(a, b) },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	started := time.Now()
	s := &Server{
		space:  sp,
		author: opts.Author,
		tmpl:   tmpl,
		now:    time.Now,

		// A person clicking as fast as they can manages a few requests a
		// second; a board with a hundred cards is one request. Signing in is
		// held far tighter, because every attempt is a call to the git host.
		reads:       newLimiter(600, 120, started),
		changes:     newLimiter(120, 30, started),
		signIns:     newLimiter(10, 5, started),
		behindProxy: opts.BehindProxy,

		deviceClientID: strings.TrimSpace(opts.DeviceClientID),
		onLoopback:     opts.OnLoopback && !opts.BehindProxy,
	}
	recheck, life := opts.Recheck, opts.SessionLife
	if recheck <= 0 {
		recheck = 5 * time.Minute
	}
	if life <= 0 {
		life = 12 * time.Hour
	}
	s.recheck = recheck

	// An authority exists when anybody can be asked about anybody: either a
	// host was named, or a repository has a remote that says who vouches for
	// it. A space where nothing can be asked runs unauthenticated, and says so.
	if !opts.Unauthenticated {
		repos, err := newRepositories(sp, opts.Host, recheck)
		if err != nil {
			return nil, err
		}
		if opts.Host != nil || anyHost(repos) {
			s.auth = newAuthority(repos, life)
		}
	}
	return s, nil
}

// config is the vocabulary of the whole space: one vault's own, or the union of
// several. See space.Config for why it is a union rather than a shared file.
func (s *Server) config() (*project.Config, error) { return s.space.Config() }

// entries is every task the person asking may see, with paths said from the
// space root.
//
// It takes the request rather than being a plain reader, so that filtering by
// what somebody may see is what happens by default and reading past it has to
// be written out. A workspace can span repositories on different hosts, and a
// board that listed tasks out of a repository the reader has no access to would
// be leaking the one thing the host was asked about.
func (s *Server) entries(r *http.Request) ([]vault.Entry, error) {
	all, err := s.space.Entries()
	if err != nil {
		return nil, err
	}
	return visible(standingIn(r), all), nil
}

// visible keeps the entries whose project the reader may see. A nil standing —
// no authority — sees everything, which is what --auth none means.
func visible(st *standing, all []vault.Entry) []vault.Entry {
	if st == nil {
		return all
	}
	kept := make([]vault.Entry, 0, len(all))
	for _, e := range all {
		if st.CanRead(e.Project) {
			kept = append(kept, e)
		}
	}
	return kept
}

// abs turns a path in the space into a path on disk, refusing one that belongs
// to no repository.
func (s *Server) abs(inSpace string) (string, error) { return s.space.Path(inSpace) }

// configFor is the vocabulary that decides what may happen to one task: its own
// project's, not the space's.
//
// The space's configuration is a union, so that a board can draw a column for
// every status any project uses. Validating against the union would let a task
// move to a status its project has never heard of, which is a board deciding
// something a repository is supposed to decide about itself.
func (s *Server) configFor(key string) (*project.Config, error) {
	projectKey, _, err := project.SplitKey(key)
	if err != nil {
		return nil, err
	}
	c, _, err := s.space.ConfigOf(projectKey)
	return c, err
}

// index is what every wikilink in the space can point at.
//
// Built across every repository, so a link from one project to a page in
// another resolves here exactly as it does in Obsidian, where the workspace is
// one vault. It is rebuilt per request: a cache here would start serving a
// vault that no longer exists.
func (s *Server) index() (*index, error) {
	ix := &index{targets: map[string]string{}}
	for _, v := range s.space.Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			return nil, err
		}
		if err := buildIndex(ix, v.Root, v.Prefix, c); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

// create writes a new task into the repository that owns its project, and
// answers with the path said from the space root.
//
// Which repository is not a choice the caller makes: a project lives in exactly
// one, and putting a task anywhere else would make the project something you
// could no longer hand over as a clone.
func (s *Server) create(opts vault.NewOptions) (string, *task.Task, error) {
	c, err := s.config()
	if err != nil {
		return "", nil, err
	}
	if opts.Project == "" {
		if keys := c.ProjectKeys(); len(keys) > 0 {
			opts.Project = keys[0]
		}
	}

	owner, v, err := s.space.ConfigOf(opts.Project)
	if err != nil {
		return "", nil, err
	}
	rel, t, err := vault.Create(v.Root, owner, opts)
	if err != nil {
		return "", nil, err
	}
	return v.PathIn(rel), t, nil
}

// pages is every page in the space, said from the space root.
// titled is a page and what to call it.
type titled struct {
	// Path is where the page is, from the space root, without .md.
	Path string
	// Title is its frontmatter title, empty when it has none.
	Title string
}

// pagesTitled is every page with the title it gives itself.
//
// A page is not required to be named after its title — that rule is for tasks,
// whose file name carries the key and the title (ADR-0005). A page may be
// `0004-access-comes-from-git.md` and call itself "Access comes from git", and
// a list of pages somebody reads should say the second.
//
// It reads each page's frontmatter, which is a small file per page and few of
// them. task.Parse does it, because a page's frontmatter is the same kind of
// thing and parsing it twice two ways is how they come to disagree.
func (s *Server) pagesTitled() []titled {
	paths := s.pages()
	out := make([]titled, 0, len(paths))
	for _, rel := range paths {
		page := titled{Path: rel}
		if full, err := s.abs(rel + ".md"); err == nil {
			if raw, err := os.ReadFile(full); err == nil {
				if t, err := task.Parse(raw); err == nil {
					page.Title = strings.TrimSpace(t.Title)
				}
			}
		}
		out = append(out, page)
	}
	return out
}

func (s *Server) pages() []string {
	var paths []string
	for _, v := range s.space.Vaults() {
		docs := filepath.Join(v.Root, vault.DocsDir)
		_ = filepath.WalkDir(docs, func(full string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(full, ".md") {
				return nil
			}
			rel, err := filepath.Rel(v.Root, full)
			if err != nil {
				return nil
			}
			paths = append(paths, strings.TrimSuffix(v.PathIn(filepath.ToSlash(rel)), ".md"))
			return nil
		})
	}
	sort.Strings(paths)
	return paths
}

// commit records a change, putting each path in the repository that owns it.
//
// A change can touch two repositories at once — a retitle in one project
// repointing a link in another — and each of them gets its own commit, because
// each of them is its own history.
func (s *Server) commit(paths []string, message string, author gitvcs.Author) error {
	byVault := map[*space.Vault][]string{}
	for _, p := range paths {
		v, rel, err := s.space.Resolve(p)
		if err != nil {
			return err
		}
		byVault[v] = append(byVault[v], rel)
	}
	for _, v := range s.space.Vaults() {
		if in := byVault[v]; len(in) > 0 {
			if err := v.Repo.Commit(in, message, author); err != nil {
				return err
			}
		}
	}
	return nil
}

// Handler routes every request the server answers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleBoard)
	mux.HandleFunc("GET /task/{key}", s.handleTask)
	mux.HandleFunc("POST /task/{key}/status", s.handleMove)
	mux.HandleFunc("POST /task/{key}/comment", s.handleComment)
	mux.HandleFunc("GET /task/{key}/edit", s.handleEditForm)
	mux.HandleFunc("GET /task/{key}/history", s.handleHistory)
	mux.HandleFunc("POST /task/{key}/edit", s.handleEdit)
	mux.HandleFunc("POST /task/{key}/attach", s.handleAttach)
	mux.HandleFunc("POST /task/{key}/delete", s.handleDeleteTask)
	mux.HandleFunc("GET /file/{path...}", s.handleFile)
	mux.HandleFunc("GET /new", s.handleNewForm)
	mux.HandleFunc("POST /new", s.handleNew)
	mux.HandleFunc("GET /pages", s.handlePages)
	mux.HandleFunc("GET /releases", s.handleReleases)
	mux.HandleFunc("GET /branches", s.handleBranches)
	mux.HandleFunc("GET /branch/{ref}", s.handleBranch)
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
	mux.HandleFunc("POST /admin/sign-in", s.handleSignInSetup)
	mux.HandleFunc("GET /sign-in", s.handleSignInForm)
	mux.HandleFunc("POST /sign-in", s.handleSignIn)
	mux.HandleFunc("POST /sign-in/device", s.handleDeviceStart)
	mux.HandleFunc("GET /sign-in/device", s.handleDeviceWait)
	mux.HandleFunc("POST /sign-in/device/stop", s.handleDeviceStop)
	mux.HandleFunc("POST /sign-in/local", s.handleLocalSignIn)
	mux.HandleFunc("POST /sign-out", s.handleSignOut)

	mux.HandleFunc("GET /api/tasks", s.apiListTasks)
	mux.HandleFunc("POST /api/tasks", s.apiCreateTask)
	mux.HandleFunc("GET /api/tasks/{key}", s.apiGetTask)
	mux.HandleFunc("PATCH /api/tasks/{key}", s.apiPatchTask)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /static/", http.FileServerFS(assets))

	// Outermost first: headers and a body cap apply to everything, including
	// what the rate limiter refuses; the rate limit applies before any work is
	// done; the cross-site check runs before the identity check, so a forged
	// request is refused for what it is rather than for who sent it.
	return s.harden(s.meter(s.checkOrigin(s.guard(mux))))
}

// version identifies the exact bytes a client saw, so a write can refuse to
// land on top of a change it never knew about.
func version(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

// keyOf is the task key from the URL.
func keyOf(r *http.Request) string { return r.PathValue("key") }

// locate finds a task's file: where it is in the space, and where it is on
// disk. The key stopped being the path in ADR-0005, and with a workspace it is
// not even in a known repository, so this is a lookup — kept in one place so
// nothing else has to know.
func (s *Server) locate(key string) (rel, full string, err error) {
	v, inVault, inSpace, err := s.space.Locate(key)
	if err != nil {
		return "", "", err
	}
	return inSpace, v.Abs(inVault), nil
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

	owner, wasIn, rel, err := s.space.Locate(key)
	if err != nil {
		return err
	}
	path := owner.Abs(wasIn)
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

	// A title lives in the file name, so changing it moves the file — and every
	// link that pointed at the old name has to move with it. All of them go
	// into one commit: git records the rename, and no point in the history has
	// the vault pointing at a note that is not there.
	//
	// Repointing is confined to the repository that owns the task. A link from
	// another project is a link across repositories, and rewriting somebody
	// else's file because a title changed here is not a rename, it is an edit
	// to a project this one does not own. `docket check` reports it there.
	paths := append([]string{rel}, companions...)
	if projectKey, _, err := project.SplitKey(t.Key); err == nil {
		if wanted := vault.PathFor(projectKey, t.Key, t.Title); wanted != wasIn {
			touched, err := vault.Retitle(owner.Root, wasIn, wanted)
			if err != nil {
				return err
			}
			for _, p := range touched {
				paths = append(paths, owner.PathIn(p))
			}
		}
	}

	return s.commit(paths, message, author)
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
