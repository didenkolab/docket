package server

import (
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Who work can be given to.
//
// Three answers, and they are not the same list. Whoever already carries work
// here is what the files say. Whoever has a page is who the vault has written
// down. Whoever has access to the repository is what the host knows — and that
// is the one somebody means by "make it theirs": a person is given rights on
// the repository, and from that moment the work can be put on them, before they
// have ever touched a task.
//
// The three are merged for the suggestion list and kept separate underneath,
// because only one of them can be trusted offline. The files are the record;
// the host is a source of candidates. So the moment work is put on somebody the
// host named, their page is written — and from then on the vault, Obsidian and
// a clone with no network all agree about who that is.

// candidate is somebody work can be given to, and where the name came from.
type candidate struct {
	Handle string
	Name   string
	// Member says the host has this person on the repository.
	Member bool
	// Page says the vault has written them down.
	Page bool
	// Carries is how many tasks are already on them.
	Carries int
}

// Called is what to show.
func (c candidate) Called() string {
	if c.Name != "" && c.Name != c.Handle {
		return c.Name + " (" + c.Handle + ")"
	}
	return c.Handle
}

// members is the collaborator list, remembered for a while.
//
// A page that offers an assignee is drawn constantly, and asking a git host who
// has access on every one of them would be a request to github.com per board
// render. The list changes when somebody is given rights, which is a thing that
// happens a few times a year.
type members struct {
	mu     sync.Mutex
	byHost map[string]memberList
}

type memberList struct {
	people []access.Collaborator
	read   time.Time
}

// memberLife is how stale the list may be. Long, because it is a list of
// colleagues rather than a permission: what somebody may *do* is rechecked on
// its own schedule — see recheck — and this only decides whose name is offered.
const memberLife = 30 * time.Minute

// membersFor is who the hosts say has access to the repositories in this space.
func (s *Server) membersFor(r *http.Request) []access.Collaborator {
	if s.auth == nil {
		return nil
	}
	var out []access.Collaborator
	seen := map[string]bool{}

	for _, repo := range s.auth.repositories() {
		if repo.host == nil {
			continue
		}
		token, held := s.tokenFor(r, repo.host.HostName())
		if !held {
			continue
		}
		for _, person := range s.membersOf(r, repo.hostKey, repo.host, token) {
			if key := strings.ToLower(person.Login); !seen[key] {
				seen[key] = true
				out = append(out, person)
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Login < out[b].Login })
	return out
}

func (s *Server) membersOf(r *http.Request, hostKey string, host access.Host, token string) []access.Collaborator {
	s.members.mu.Lock()
	if known, ok := s.members.byHost[hostKey]; ok && s.now().Sub(known.read) < memberLife {
		s.members.mu.Unlock()
		return known.people
	}
	s.members.mu.Unlock()

	// Outside the lock: this is a network call, and holding a mutex across one
	// would make every page wait for the slowest host.
	people, err := host.Collaborators(r.Context(), token)
	if err != nil {
		// Most hosts only answer this for an administrator. Not being told is
		// the ordinary case and not a fault: the suggestion list is then what
		// the files say, which is what it was before any of this.
		people = nil
	}

	s.members.mu.Lock()
	if s.members.byHost == nil {
		s.members.byHost = map[string]memberList{}
	}
	s.members.byHost[hostKey] = memberList{people: people, read: s.now()}
	s.members.mu.Unlock()
	return people
}

// candidates is everybody work can be given to, best first.
func (s *Server) candidates(r *http.Request) []candidate {
	byHandle := map[string]*candidate{}
	at := func(handle string) *candidate {
		key := strings.ToLower(handle)
		if known, ok := byHandle[key]; ok {
			return known
		}
		byHandle[key] = &candidate{Handle: handle}
		return byHandle[key]
	}

	if entries, err := s.entries(r); err == nil {
		for handle, count := range vault.HandlesIn(entries) {
			at(handle).Carries = count
		}
	}
	for _, p := range s.peopleIn() {
		c := at(p.Handle)
		c.Page, c.Name = true, p.Name
	}
	for _, m := range s.membersFor(r) {
		c := at(m.Login)
		c.Member = true
		if c.Name == "" {
			c.Name = m.Name
		}
	}

	out := make([]candidate, 0, len(byHandle))
	for _, c := range byHandle {
		out = append(out, *c)
	}
	// Who is already doing the work first, then who the vault has written down,
	// then everybody else the host knows. A list ordered by how likely the next
	// click is.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Carries != out[b].Carries {
			return out[a].Carries > out[b].Carries
		}
		if out[a].Page != out[b].Page {
			return out[a].Page
		}
		return strings.ToLower(out[a].Handle) < strings.ToLower(out[b].Handle)
	})
	return out
}

// peopleIn is the person pages of every vault in the space.
func (s *Server) peopleIn() []vault.Person {
	var out []vault.Person
	for _, v := range s.sp().Vaults() {
		people, err := vault.People(v.Root)
		if err != nil {
			continue
		}
		out = append(out, people...)
	}
	return out
}

// writePersonPage writes somebody down, and reports the path it wrote, or "" if
// they were already there.
//
// Called when work is put on somebody the host named but the vault has not. A
// link to a note that does not exist is a dead link in Obsidian — the graph
// draws it as a ghost and the backlinks pane has nowhere to show the work — so
// the page is written in the same commit as the assignment rather than left for
// `docket check` to complain about later.
func (s *Server) writePersonPage(handle string, who access.Collaborator, hostKey string) (string, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return "", nil
	}
	if _, ok := vault.PersonOf(s.peopleIn(), handle); ok {
		return "", nil
	}

	vaults := s.sp().Vaults()
	if len(vaults) == 0 {
		return "", nil
	}
	// The first vault, which is the one a board is of. A workspace holding
	// several is a workspace where people are shared, and writing the page four
	// times would make four people.
	v := vaults[0]

	rel := filepath.Join(vault.PeopleDir, handle+".md")
	full := filepath.Join(v.Root, rel)
	if _, err := os.Stat(full); err == nil {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}

	page := vault.PersonPage(handle, who.Name)
	if who.Login != "" {
		page = withHostLogin(page, hostKey, who.Login)
	}
	if err := os.WriteFile(full, []byte(page), 0o644); err != nil {
		return "", err
	}
	return v.PathIn(rel), nil
}

// withHostLogin adds the sign-in handle to a freshly written page, so that
// whoever signs in as that login is recognised as this person.
func withHostLogin(page, hostKey, login string) string {
	field := "gitlab"
	if strings.Contains(strings.ToLower(hostKey), "github") {
		field = "github"
	}
	const front = "type: person\n"
	if !strings.Contains(page, front) {
		return page
	}
	return strings.Replace(page, front, front+field+": "+login+"\n", 1)
}

// memberNamed finds a collaborator by login.
func memberNamed(people []access.Collaborator, login string) (access.Collaborator, bool) {
	for _, p := range people {
		if strings.EqualFold(p.Login, login) {
			return p, true
		}
	}
	return access.Collaborator{}, false
}

// hostKeyFor is the host the person being assigned came from, when one did.
func (s *Server) hostKeyFor(r *http.Request, login string) string {
	if s.auth == nil {
		return ""
	}
	for _, repo := range s.auth.repositories() {
		if repo.host == nil {
			continue
		}
		token, held := s.tokenFor(r, repo.host.HostName())
		if !held {
			continue
		}
		if _, ok := memberNamed(s.membersOf(r, repo.hostKey, repo.host, token), login); ok {
			return repo.hostKey
		}
	}
	return ""
}

// meAs is the handle of whoever is reading, as the vault would write it.
//
// The host's login, not their display name: a login is unique on the host and
// stable, which is what a handle has to be. A server with nobody signed in has
// no answer, and offers no button rather than guessing.
func (s *Server) meAs(r *http.Request) string {
	st := standingIn(r)
	if st == nil || !st.SignedIn {
		return ""
	}
	if login := strings.TrimSpace(st.Best.Login); login != "" {
		return login
	}
	return ""
}

// ---- their page ----

type personView struct {
	Handle string
	Name   string
	// Path is the file, so the page can say where it is and link to editing it.
	Path   string
	GitHub string
	GitLab string
	Body   template.HTML
	// Open is what they are carrying, and Done is what they have finished,
	// most recently changed first.
	Open  []personTask
	Done  []personTask
	Board string
	// Member says the host still lists them on the repository. A person page
	// outlives access, which is right — history keeps who did what — but a
	// board offering somebody who left is a board that misassigns work.
	Member bool
}

type personTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	Href     string
	Project  string
}

// handlePerson shows one person: who they are, and what is on them.
func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	handle := strings.TrimSpace(r.PathValue("handle"))
	if handle == "" {
		http.Redirect(w, r, "/people", http.StatusSeeOther)
		return
	}

	entries, err := s.entries(r)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}
	carried := vault.AssignedTo(entries, handle)

	person, hasPage := vault.PersonOf(s.peopleIn(), handle)
	if !hasPage && len(carried) == 0 {
		s.fail(w, r, http.StatusNotFound, "Nobody by that name",
			"No page in "+vault.PeopleDir+"/ is "+handle+", and no task is on them.")
		return
	}

	view := personView{
		Handle: handle, Name: person.Name, Path: person.Path,
		GitHub: person.GitHub, GitLab: person.GitLab,
		Board: "/?assignee=" + url.QueryEscape(handle),
	}
	if _, ok := memberNamed(s.membersFor(r), handle); ok {
		view.Member = true
	}
	if person.Body != "" {
		if ix, err := s.index(); err == nil {
			view.Body = renderMarkdown(person.Body, ix)
		}
	}

	for _, e := range carried {
		shown := personTask{
			Key: e.Key, Title: e.Task.Title, Status: e.Task.Status,
			Category: e.Task.StatusCategory, Href: "/task/" + e.Key, Project: e.Project,
		}
		if e.Task.StatusCategory == project.CategoryDone {
			view.Done = append(view.Done, shown)
			continue
		}
		view.Open = append(view.Open, shown)
	}
	// What is finished is an archive, and the same rule as a board's finished
	// column applies: the recent part, or the page is a wall of history.
	sort.SliceStable(view.Done, func(a, b int) bool { return view.Done[a].Key > view.Done[b].Key })
	if len(view.Done) > recentlyDone {
		view.Done = view.Done[:recentlyDone]
	}

	title := handle
	if person.Name != "" {
		title = person.Name
	}
	s.render(w, r, "person.html", c, title, view)
}

// handlePeople lists everybody.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	s.render(w, r, "people.html", c, "People", s.candidates(r))
}
