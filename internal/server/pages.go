package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

type pageData struct {
	Config *project.Config
	Title  string
	Data   any
	// You is who is asking, so the interface can offer only what they may do.
	You      access.Identity
	SignedIn bool
	// CSRF is what this page must send back with a change for the change to be
	// accepted. Every form carries it; the scripts read it from the head.
	CSRF string
	// Theme is the colour scheme this request asked for, stamped on the html
	// element so the page arrives in the right colours rather than changing
	// after it paints. Empty means follow the system. See theme.go.
	Theme  string
	Themes []themeChoice
	Here   string
	// Workspace says this space holds several repositories, so there is a
	// Projects page worth offering.
	Workspace bool
	// Sprints says the vault has sprint pages, so there is a Sprints page worth
	// a place in the navigation.
	//
	// Conditional because most vaults will not use them and a navigation with
	// one more permanent item is a navigation that fits on fewer screens. A
	// sprint is offered to teams that work in sprints, and the way a vault says
	// it works in sprints is by having written one down.
	Sprints bool
	// People says somebody in this space has a page, so there is a People page
	// worth a place in the navigation. A vault that keeps no people does not
	// get a menu item for an empty list.
	People bool
	// Views says the vault has saved views — boards/*.base — to offer. Every
	// scaffolded vault has some, so this is all but always true; it is asked
	// rather than assumed so that a vault whose boards folder was deleted does
	// not offer a page of nothing.
	Views bool
	// Pushes are the repositories with something unsent, or something that went
	// wrong sending it. Empty when everything is where everybody else can read
	// it, because a badge that is always there is a badge nobody reads.
	Pushes []pushNote
	// Sees says the reader may look at something here. False only on a server
	// that signs people in, for somebody who has not — and then the navigation
	// has nothing to offer, because every link in it would bounce them back.
	Sees bool
	// Refresh, when set, makes the page reload itself after that many seconds.
	// One page needs it — the one waiting for somebody to type a code on
	// GitHub — and it is a meta tag rather than a script so that waiting works
	// in a browser with scripting off, like everything else here.
	Refresh int
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string,
	c *project.Config, title string, data any) {
	s.renderEvery(0, w, r, name, c, title, data)
}

// renderEvery is render for a page that has to keep looking: after seconds, the
// browser asks for it again.
func (s *Server) renderEvery(seconds int, w http.ResponseWriter, r *http.Request, name string,
	c *project.Config, title string, data any) {

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	you, signedIn := s.whoIsAsking(r)
	page := pageData{
		Config: c, Title: title, Data: data,
		You:       you,
		SignedIn:  signedIn,
		CSRF:      tokenOf(r),
		Sees:      s.showsAnything(r),
		Pushes:    s.pushNotes(),
		Workspace: s.sp().Workspace,
		Sprints:   len(s.sp().Sprints()) > 0,
		Views:     len(s.sp().Views()) > 0,
		People:    len(s.peopleIn()) > 0,
		Theme:     themeAttribute(themeOf(r)),
		Themes:    themeChoices(themeOf(r)),
		Here:      r.URL.RequestURI(),
		Refresh:   seconds,
	}
	if err := s.tmpl.ExecuteTemplate(w, name, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// whoIsAsking is what the page should assume about the person reading it.
//
// The sign-in page is reachable without a session, and it used to be drawn as
// though whoever loaded it were an administrator: the guard puts no identity on
// an open path, and the default for "nobody said" is the widest role, which is
// right for a server with no authority and wrong here. So on a server that does
// sign people in, no identity means no role — an empty nav and no claim about
// who you are — and only a real session says otherwise.
// showsAnything reports whether the navigation has anywhere to send the reader.
//
// A nil standing means two different things and they must not be confused: on a
// server with nobody to sign in it means everything is visible, and on a server
// that signs people in it means this request has not — the guard puts no
// standing on the sign-in page, which is an open path. Only the server knows
// which it is.
func (s *Server) showsAnything(r *http.Request) bool {
	if s.auth == nil {
		return true
	}
	st := standingIn(r)
	return st != nil && st.Sees()
}

func (s *Server) whoIsAsking(r *http.Request) (access.Identity, bool) {
	if s.auth == nil {
		return identityOf(r), false
	}
	st := standingIn(r)
	if st == nil {
		return access.Identity{}, false
	}
	return st.Best, st.SignedIn
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, title, message string) {
	c, _ := s.config()
	w.WriteHeader(code)
	s.render(w, r, "error.html", c, title, message)
}

// ---- board ----

type column struct {
	Status project.Status
	Cards  []card
	// Size is what the column's cards add up to, in the vault's unit, or empty
	// when the vault does not size work or nothing in the column is sized.
	//
	// A column head is where "how much is in progress" is actually asked, and a
	// work-in-progress limit is a number about a column. Counting cards answers
	// neither: three cards of thirteen points is not the same column as three
	// cards of one.
	Size string
	// Unsized is how many of its cards nobody has estimated, so the total is
	// never read as the whole.
	Unsized int
	// Count is how many cards are in the column, which is not always how many
	// are drawn. The head says this one: a number that shrank because the page
	// decided not to draw something is a number that lies.
	Count int
	// Hidden is how many of them are not drawn, and MoreHref is where the whole
	// column is. LessHref is the way back, set only on a column being shown
	// whole.
	Hidden   int
	MoreHref string
	LessHref string
}

type card struct {
	Key      string
	Href     string
	Project  string
	Title    string
	Status   string
	Assignee string
	Priority string
	Labels   []string
	Tags     []string
	// Size is the estimate as written, or empty when nobody has said. On the
	// card because it is half of what a card is picked up for.
	Size string
	// Notable says the priority is worth showing.
	//
	// A real board came out with "medium" on every one of eleven hundred cards,
	// which is the badge-that-is-always-there again: it costs a line on every
	// card and tells nobody anything. The default is the value that says
	// "nobody has thought about this", so the ones worth seeing are the others.
	Notable bool
	// Sprint is the sprint this card is in, when the vault runs them.
	Sprint string
	// Blocked is set when something this card waits on is unfinished. The one
	// relation that changes what somebody picks up next, so it is on the card.
	Blocked bool
	// Epic is the container this card belongs to, when the vault has levels and
	// the card has a parent above it. Jira puts this on the card because "which
	// larger thing is this part of" is the question a board is scanned for.
	Epic      string
	EpicTitle string
	// order is where somebody put this card in its column, if anybody has.
	// Cards without one follow the ones with, in key order.
	order *int
	// updated is when the file last changed, as written. It decides which of a
	// finished column's cards are the ones still worth drawing.
	updated string
	// Version is the fingerprint of the file this card was rendered from. The
	// board hands it back when a card is dragged, so a drop lands on the file
	// the person actually saw.
	Version string
	// Reachable is where the workflow lets this card go, so a drag can refuse
	// a column before the drop rather than after it.
	Reachable string
}

type boardView struct {
	Columns  []column
	Broken   []vault.Entry
	Projects []projectTab
	Selected string
	Total    int
	// Where a first task would go, for a vault that has none yet. A board with
	// nothing on it should say what to do rather than repeat "nothing here"
	// once per column.
	FirstProject string
	FirstKey     string
	// Ref is the branch this board is drawn from, empty for the working tree.
	// A board showing a proposal has to say so, or somebody acts on a plan
	// nobody has agreed to.
	Ref string

	// Menus is the filter bar: who the cards are for, and which columns are
	// drawn. The same menus the search page uses, because they are the same
	// question asked in two places.
	Menus []finderMenu
	// Narrowed says the board is not showing everything, and Cleared is where
	// to go to see it all. A board that quietly hid work would be worse than no
	// filter at all.
	Narrowed bool
	Cleared  string
	// Hidden is how many cards the filter left out, so the narrowing is a
	// number rather than a feeling.
	Hidden int
}

// sortCards puts a column in the order somebody dragged it into.
//
// A card with no order follows every card that has one, in the order the vault
// listed them — by key, which is by age. So a column nobody has touched reads
// oldest first, and dragging one card to the top does not renumber the rest.
func sortCards(cards []card) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i].order, cards[j].order
		switch {
		case a != nil && b != nil:
			return *a < *b
		case a != nil:
			return true
		default:
			return false
		}
	})
}

// totalOf adds up a column, and says how much of it is unaccounted for.
//
// The total is what the cards say about themselves. A container carries no
// estimate — rule 12 refuses one, because its size is what its children add to
// — so it contributes nothing and nothing is counted twice.
func totalOf(cards []card, c *project.Config) (total string, unsized int) {
	if !c.Sizes() {
		return "", 0
	}
	var sum float64
	for _, drawn := range cards {
		if drawn.Size == "" {
			unsized++
			continue
		}
		sum += amount(drawn.Size)
	}
	if sum == 0 {
		return "", unsized
	}
	return project.Amount(sum), unsized
}

// recentlyDone is how much of a finished column a board draws.
//
// A real board arrived with 573 cards in "Готово" out of 1096 — half the page,
// a megabyte of it, spent on work nobody is going to pick up. A board is for
// the work that is moving; the archive is a thing you go and ask for. Twenty
// five is about a screen of scrolling, which is as far as anybody reads back
// before they would rather search.
const recentlyDone = 25

// holdBack draws only the recently finished part of a finished column.
//
// Only the done category, because that is the only column whose contents stop
// changing: a long "In progress" is a fact about the team and hiding it would
// be hiding the fact. And only ever a default — the column says how many it is
// not drawing and links to the rest, so nothing is quietly gone.
func holdBack(col *column, whole string, href func(string) string) {
	if col.Status.Category != project.CategoryDone || col.Count <= recentlyDone {
		return
	}
	if whole == col.Status.Name {
		col.LessHref = href("")
		return
	}

	// By when it last changed rather than by the order somebody dragged it
	// into: nobody arranges an archive, and the part of it worth seeing is the
	// part that happened this week. A task nobody has dated sorts last, which
	// is where "no idea when" belongs.
	kept := append([]card(nil), col.Cards...)
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].updated > kept[j].updated })

	col.Cards = kept[:recentlyDone]
	col.Hidden = col.Count - recentlyDone
	col.MoreHref = href(col.Status.Name)
}

// elsewhere is this address with one query parameter set, or removed when the
// value is empty.
func elsewhere(at *url.URL, key, value string) string {
	q := at.Query()
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	if len(q) == 0 {
		return at.Path
	}
	return at.Path + "?" + q.Encode()
}

// worksFor reports whether a card belongs to the person the board is narrowed
// to. "!unassigned" is nobody, which is the one filter a board is asked for
// most: what has not been picked up.
func worksFor(wanted, assignee string) bool {
	switch wanted {
	case "":
		return true
	case "!unassigned":
		return strings.TrimSpace(assignee) == ""
	}
	return assignee == wanted
}

// boardMenus is the filter bar: whose work, and which column.
//
// The same two menus the search page has, built here rather than shared with
// it, because a board narrows by different things and by fewer of them: the
// bar is meant to be read at a glance above a wall of cards, and seven
// dimensions above a board would be a form sitting on top of the work.
func boardMenus(at *url.URL, c *project.Config, people map[string]int,
	forWhom, atStatus string) []finderMenu {

	who := finderMenu{Name: "Assignee", Value: forWhom, Shown: shownAs("Assignee", forWhom)}
	who.Clear = elsewhere(at, "assignee", "")
	who.Options = []finderOption{{Label: "Anyone", Href: who.Clear, On: forWhom == ""}}
	if people[""] > 0 {
		who.Options = append(who.Options, finderOption{
			Label: "Nobody", Href: elsewhere(at, "assignee", "!unassigned"),
			On: forWhom == "!unassigned",
		})
	}
	var names []string
	for name := range people {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		who.Options = append(who.Options, finderOption{
			Label: name, Href: elsewhere(at, "assignee", name), On: name == forWhom,
		})
	}

	where := finderMenu{Name: "Status", Value: atStatus, Shown: shownAs("Status", atStatus)}
	where.Clear = elsewhere(at, "status", "")
	where.Options = []finderOption{{Label: "Every column", Href: where.Clear, On: atStatus == ""}}
	for _, status := range c.Statuses {
		where.Options = append(where.Options, finderOption{
			Label: status.Name, Href: elsewhere(at, "status", status.Name),
			On: status.Name == atStatus,
		})
	}

	return []finderMenu{who, where}
}

// without is this address with those query parameters dropped.
func without(at *url.URL, keys ...string) string {
	q := at.Query()
	for _, key := range keys {
		q.Del(key)
	}
	if len(q) == 0 {
		return at.Path
	}
	return at.Path + "?" + q.Encode()
}

// reachableList is the workflow, flattened for an attribute.
func reachableList(c *project.Config, from string) string {
	var names []string
	for _, s := range c.Reachable(from) {
		names = append(names, s.Name)
	}
	return strings.Join(names, "\n")
}

type projectTab struct {
	Key   string
	Name  string
	Count int
	Href  string
	On    bool
}

// handleBoard shows every project at once, or one of them.
//
// Work crosses projects constantly, so the default is all of them and the
// project is a chip on the card rather than a separate board to go and find.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	s.board(w, r, s.sp(), "")
}

// board draws the board from one space, which is the working tree or a branch.
//
// Reading a branch goes through here rather than through a ref threaded into
// every helper: a proposal is looked at, not worked in, and a separate way in
// is a way that cannot be forgotten. See handleBranch.
func (s *Server) board(w http.ResponseWriter, r *http.Request, sp *space.Space, ref string) {
	c, err := sp.Config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := sp.Entries()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}
	// Filtered here rather than through s.entries, because a board can be drawn
	// over any space — the working tree, or a branch — and the one being read is
	// the argument. What may be seen is the same question either way.
	entries = visible(standingIn(r), entries)

	selected := r.URL.Query().Get("project")
	if selected != "" && !c.HasProject(selected) {
		s.fail(w, r, http.StatusNotFound, "No such project",
			selected+" is not in this vault: it holds "+strings.Join(c.ProjectKeys(), ", "))
		return
	}

	// The columns of the project being looked at, not of everything in the
	// space. A workspace's statuses are the union of its projects' — which is
	// the only honest answer for "every project", and the wrong one for a tab:
	// a board of eleven hundred Acme tasks came out with twenty one columns,
	// fifteen of them belonging to somebody else's workflow and permanently
	// empty. A column a card cannot be dropped into is a column in the way.
	drawn := c
	if selected != "" {
		if own, _, err := sp.ConfigOf(selected); err == nil {
			drawn = own
		}
	}

	// Narrowing the board is asking two questions of it: whose work, and which
	// part of the workflow. Both are one value, and both are in the address, so
	// a narrowed board is a link somebody can send.
	forWhom := r.URL.Query().Get("assignee")
	atStatus := r.URL.Query().Get("status")

	view := boardView{
		Selected: selected, Ref: ref,
		Narrowed: forWhom != "" || atStatus != "",
		Cleared:  without(r.URL, "assignee", "status"),
	}
	for _, status := range drawn.Statuses {
		// A status filter draws that column and no other. The columns are the
		// workflow, so narrowing to one is how "just show me what is in review"
		// is asked of a board of fourteen of them.
		if atStatus != "" && status.Name != atStatus {
			continue
		}
		view.Columns = append(view.Columns, column{Status: status})
	}

	// Where a card may go is its own project's business, and in a workspace the
	// projects disagree — a column drawn because one project has it is a column
	// another project's cards must not be draggable into. Read once per
	// project rather than once per card.
	vocabulary := map[string]*project.Config{}
	configOf := func(projectKey string) *project.Config {
		if known, ok := vocabulary[projectKey]; ok {
			return known
		}
		own, _, err := sp.ConfigOf(projectKey)
		if err != nil {
			own = c
		}
		vocabulary[projectKey] = own
		return own
	}

	// Every task by key, so a card can say whether what it waits on is done.
	known := map[string]vault.Entry{}
	for _, e := range entries {
		if e.Task != nil {
			known[e.Key] = e
		}
	}

	counts := map[string]int{}
	// Who has work here, counted before the filter so that the menu offers
	// everybody on the board rather than only the person already chosen.
	people := map[string]int{}
	for _, e := range entries {
		if e.Err != nil {
			view.Broken = append(view.Broken, e)
			continue
		}
		counts[e.Project]++
		if selected != "" && e.Project != selected {
			continue
		}
		people[e.Task.Assignee]++
		if !worksFor(forWhom, e.Task.Assignee) {
			view.Hidden++
			continue
		}

		// The container this card is part of, if the vault says what its levels
		// are and this card's parent is above it.
		epic, epicTitle := "", ""
		if own := configOf(e.Project); own.Layered() && e.Task.Parent != "" {
			if parent, ok := known[e.Task.Parent]; ok && parent.Task != nil &&
				own.LevelOf(parent.Task.Type) > own.LevelOf(e.Task.Type) {
				epic, epicTitle = parent.Key, parent.Task.Title
			}
		}
		placed := false
		for i := range view.Columns {
			if view.Columns[i].Status.Name == e.Task.Status {
				drawn := card{
					Key: e.Key, Href: "/task/" + e.Key, Project: e.Project,
					Title: e.Task.Title, Status: e.Task.Status,
					Assignee: e.Task.Assignee, Priority: e.Task.Priority,
					Labels: e.Task.Labels, Tags: e.Task.Tags, Version: version(e.Raw),
					Reachable: reachableList(configOf(e.Project), e.Task.Status),
					order:     e.Task.Order,
					updated:   e.Task.Updated,
					Blocked:   blocked(e.Task, known),
					Epic:      epic, EpicTitle: epicTitle,
					Sprint: e.Task.Sprint,
				}
				if e.Task.Sized() {
					drawn.Size = project.Amount(e.Task.Size())
				}
				drawn.Notable = e.Task.Priority != configOf(e.Project).DefaultPriority()
				view.Columns[i].Cards = append(view.Columns[i].Cards, drawn)
				view.Total++
				placed = true
			}
		}
		// A card in a column the board is not drawing is a card the filter left
		// out, and the line above the board has to count it: "not shown" that
		// counts only half of what is not shown is the number that lies.
		if !placed && atStatus != "" {
			view.Hidden++
		}
	}

	whole := r.URL.Query().Get("full")
	for i := range view.Columns {
		sortCards(view.Columns[i].Cards)
		// Counted and added up before anything is put aside, so the head is
		// about the column and not about the page.
		view.Columns[i].Size, view.Columns[i].Unsized = totalOf(view.Columns[i].Cards, drawn)
		view.Columns[i].Count = len(view.Columns[i].Cards)
		holdBack(&view.Columns[i], whole, func(status string) string {
			return elsewhere(r.URL, "full", status)
		})
	}

	view.Menus = boardMenus(r.URL, drawn, people, forWhom, atStatus)

	view.Projects = append(view.Projects, projectTab{
		Key: "All", Name: "Every project", Count: len(entries), Href: "/", On: selected == "",
	})
	for _, p := range c.Projects {
		view.Projects = append(view.Projects, projectTab{
			Key: p.Key, Name: p.Name, Count: counts[p.Key],
			Href: "/?project=" + url.QueryEscape(p.Key), On: selected == p.Key,
		})
	}

	if keys := c.ProjectKeys(); len(keys) > 0 {
		view.FirstProject, view.FirstKey = keys[0], keys[0]
		if selected != "" {
			view.FirstProject, view.FirstKey = selected, selected
		}
	}

	title := c.Name
	if selected != "" {
		title = c.ProjectName(selected)
	}
	s.render(w, r, "board.html", c, title, view)
}

// ---- one task ----

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	t, ver, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	rel, _, err := s.locate(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	projectKey, _, _ := project.SplitKey(key)
	ix, err := s.index()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	view := taskView{
		Task:        t,
		Reachable:   c.Reachable(t.Status),
		Project:     projectKey,
		ProjectName: c.ProjectName(projectKey),
		Description: renderMarkdown(t.Description(), ix),
		Comments:    renderComments(t.Comments(), ix),
		Children:    s.childrenOf(r, c, key),
		Relations:   s.relationsOf(r, t),
		Backlinks:   s.backlinks(r, strings.TrimSuffix(path.Base(rel), ".md"), rel),
		Version:     ver,
		Path:        rel,
		Unit:        c.Unit(),
	}
	if t.Sized() {
		view.Size = project.Amount(t.Size())
	}
	// A container's size is what its children add to, computed here rather than
	// stored — see rule 12.
	if len(view.Children) > 0 {
		var sum float64
		sized := false
		for _, child := range view.Children {
			if child.Size != "" {
				sum += amount(child.Size)
				sized = true
			}
		}
		if sized {
			view.Rollup = project.Amount(sum)
		}
	}
	view.Fields = shownFields(c, t)
	view.People = s.candidates(r)
	view.Me = s.meAs(r)
	s.render(w, r, "task.html", c, t.Key+" "+t.Title, view)
}

// shownFields is what this vault added, for a task of this type.
func shownFields(c *project.Config, t *task.Task) []shownField {
	var out []shownField
	for _, f := range c.FieldsFor(t.Type) {
		value := strings.TrimSpace(t.Property(f.Name))
		shown := shownField{Label: f.Shown(), Value: value, Kind: f.Kind, Help: f.Help}
		switch f.Kind {
		case project.FieldLink:
			shown.Link = value != ""
		case project.FieldFlag:
			shown.Flag = true
			shown.On = strings.EqualFold(value, "true")
		}
		out = append(out, shown)
	}
	return out
}

type taskView struct {
	Task        *task.Task
	Reachable   []project.Status
	Project     string
	ProjectName string
	Description template.HTML
	Comments    []renderedComment
	Children    []childTask
	Relations   []relationGroup
	Backlinks   []mention
	Version     string
	Path        string
	// Unit is the word estimates are in, empty when the vault does not size
	// work — and then the row is not shown at all rather than shown empty.
	Unit string
	// Size is this task's own estimate, as written.
	Size string
	// Rollup is what its children add up to, for a container. When it is set it
	// is the answer, because a container has no estimate of its own.
	Rollup string
	// People is everybody the work can be given to: whoever already carries
	// some, whoever the vault has written a page for, and whoever the host says
	// has access to the repository. The last is the one that matters for
	// somebody new — rights on the repository are how a person becomes
	// somebody work can be put on, before they have touched a task.
	//
	// This was referenced by the template before it existed on this view, and a
	// missing field stops template execution where it stands — so the page was
	// rendered as far as the properties panel and simply ended. Everything
	// below it, the comments, the history and the delete button, was gone, and
	// nothing said so.
	People []candidate
	// Me is the handle of whoever is reading, when the host knows them, so the
	// work can be taken in one click.
	Me string
	// Fields are the vault's own properties for this type of task, with what
	// this one holds. Shown in the order declared, and shown even when empty:
	// a field a type has and this task does not is a fact, and hiding it is how
	// somebody never learns the field exists.
	Fields []shownField
}

// shownField is one declared property, ready to read.
type shownField struct {
	Label string
	Value string
	Kind  string
	Help  string
	// Link is set when the value is a URL, so it can be followed.
	Link bool
	// Flag is set when the value is yes or no, so it can be drawn as one.
	Flag bool
	On   bool
}

type renderedComment struct {
	Author  string
	When    string
	Text    template.HTML
	Initial string
}

type childTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	// Size is the child's estimate, which is what a container's own size is
	// made of.
	Size string
}

func renderComments(comments []task.Comment, ix *index) []renderedComment {
	out := make([]renderedComment, 0, len(comments))
	for _, c := range comments {
		initial := "?"
		if c.Author != "" {
			initial = strings.ToUpper(c.Author[:1])
		}
		out = append(out, renderedComment{
			Author: c.Author, When: c.When,
			Text: renderMarkdown(c.Text, ix), Initial: initial,
		})
	}
	return out
}

// childrenOf lists the tasks that name this one as their parent. A hierarchy
// written only downwards is a hierarchy you can only read from the wrong end.
func (s *Server) childrenOf(r *http.Request, c *project.Config, key string) []childTask {
	entries, err := s.entries(r)
	if err != nil {
		return nil
	}
	var children []childTask
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == key {
			child := childTask{
				Key: e.Key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
			}
			if e.Task.Sized() {
				child.Size = project.Amount(e.Task.Size())
			}
			children = append(children, child)
		}
	}
	return children
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	// Its own project's vocabulary decides where it may go — see configFor.
	c, err := s.configFor(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", err.Error())
		return
	}

	status := r.FormValue("status")
	category, known := c.CategoryOf(status)
	if !known {
		s.fail(w, r, http.StatusBadRequest, "Unknown status",
			status+" is not one of "+strings.Join(c.StatusNames(), ", "))
		return
	}

	author := s.authorFor(r)
	err = s.editTask(r, key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
		if t.Status == status {
			return "", nil, nil
		}
		if !c.CanMove(t.Status, status) {
			return "", nil, fmt.Errorf("the workflow does not allow %s → %s. From %s a task can go to %s",
				t.Status, status, t.Status, strings.Join(names(c.Reachable(t.Status)), ", "))
		}
		was := t.Status
		t.SetStatus(status, category)
		return key + ": " + was + " → " + status, nil, nil
	})
	s.afterEdit(w, r, key, err)
}

func (s *Server) handleComment(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
		return
	}

	author := s.authorFor(r)
	err := s.editTask(r, key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
		t.AppendComment(author.Name, s.now(), text)
		return key + ": comment from " + author.Name, nil, nil
	})
	s.afterEdit(w, r, key, err)
}

// names flattens statuses for a message.
func names(statuses []project.Status) []string {
	out := make([]string, len(statuses))
	for i, s := range statuses {
		out[i] = s.Name
	}
	return out
}

// afterEdit turns the outcome of a write into a response.
func (s *Server) afterEdit(w http.ResponseWriter, r *http.Request, key string, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
	case errors.Is(err, ErrStale):
		s.fail(w, r, http.StatusConflict, "Someone got there first",
			"This task changed on disk after the page was loaded — most likely in Obsidian or "+
				"by an agent. Nothing was written. Reload "+key+" and make the change again.")
	case os.IsNotExist(err):
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
	case strings.Contains(err.Error(), "workflow does not allow"):
		s.fail(w, r, http.StatusBadRequest, "The workflow says no", err.Error())
	default:
		s.fail(w, r, http.StatusInternalServerError, "The change was not saved", err.Error())
	}
}

// ---- creating a task ----

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	parent := r.URL.Query().Get("parent")
	selected := r.URL.Query().Get("project")
	if selected == "" && parent != "" {
		// A child belongs where its parent does, unless told otherwise.
		if key, _, err := project.SplitKey(parent); err == nil {
			selected = key
		}
	}
	s.render(w, r, "new.html", c, "New task", struct {
		Project string
		Parent  string
	}{selected, parent})
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	_, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	author := s.authorFor(r)
	s.writes.Lock()
	rel, t, err := s.create(vault.NewOptions{
		Project:     r.FormValue("project"),
		Title:       r.FormValue("title"),
		Type:        r.FormValue("type"),
		Priority:    r.FormValue("priority"),
		Assignee:    strings.TrimSpace(r.FormValue("assignee")),
		Parent:      strings.TrimSpace(r.FormValue("parent")),
		Description: normaliseNewlines(r.FormValue("body")),
		Now:         s.now(),
	})
	if err == nil {
		err = s.commit(r, []string{rel}, t.Key+": "+t.Title, author)
	}
	s.writes.Unlock()

	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "The task was not created", err.Error())
		return
	}
	http.Redirect(w, r, "/task/"+t.Key, http.StatusSeeOther)
}

// ---- knowledge base ----

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()
	view := newPagesView(s.pagesTitled())
	who, _ := s.whoIsAsking(r)
	view.Tree = offerMaking(view.Tree, who.CanWrite())
	s.render(w, r, "pages.html", c, "Pages", view)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()

	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if rel == "" || strings.HasPrefix(rel, "..") {
		s.fail(w, r, http.StatusBadRequest, "Not a page", "that path leads outside the vault")
		return
	}

	full, err := s.abs(rel + ".md")
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", err.Error())
		return
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", rel+" is not in this vault")
		return
	}
	ix, err := s.index()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	body := string(raw)
	title := path.Base(rel)
	if t, err := task.Parse(raw); err == nil {
		body = t.Body()
		if t.Title != "" {
			title = t.Title
		}
	}

	s.render(w, r, "page.html", c, title, struct {
		Path string
		Body template.HTML
	}{rel, renderMarkdown(body, ix)})
}

// ---- search ----

type hit struct {
	Href    string
	Label   string
	Context string
	// Task is empty for a page, and describes the card for a task, so a result
	// list can say what state the work is in without a second click.
	Task *hitTask
}

type hitTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	Priority string
	Assignee string
	Labels   []string
	Tags     []string
	// Size is the estimate as written, or empty when nobody has said. On the
	// card because it is half of what a card is picked up for.
	Size string
	// Notable says the priority is worth showing.
	//
	// A real board came out with "medium" on every one of eleven hundred cards,
	// which is the badge-that-is-always-there again: it costs a line on every
	// card and tells nobody anything. The default is the value that says
	// "nobody has thought about this", so the ones worth seeing are the others.
	Notable bool
	// Sprint is the sprint this card is in, when the vault runs them.
	Sprint string
	// Blocked is set when something this card waits on is unfinished. The one
	// relation that changes what somebody picks up next, so it is on the card.
	Blocked bool
	// Epic is the container this card belongs to, when the vault has levels and
	// the card has a parent above it. Jira puts this on the card because "which
	// larger thing is this part of" is the question a board is scanned for.
	Epic      string
	EpicTitle string
}

// filters is what a person narrowed the search to. Every field is empty by
// default, and an empty field matches everything.
type filters struct {
	Query    string
	Project  string
	Status   string
	Type     string
	Priority string
	Assignee string
	Label    string
	Tag      string
}

// narrowed reports whether anything but the text was asked for. It decides
// whether pages are searched at all: a page has no assignee, so a search
// narrowed by one is asking about tasks.
func (f filters) Narrowed() bool {
	return f.Project != "" || f.Status != "" || f.Type != "" ||
		f.Priority != "" || f.Assignee != "" || f.Label != "" || f.Tag != ""
}

// empty reports a form nobody has filled in yet.
func (f filters) Empty() bool { return f.Query == "" && !f.Narrowed() }

// searchView carries the vocabulary the form offers alongside the results.
// Statuses, types and priorities come from docket.yaml; assignees and labels are
// whatever the vault actually uses, because neither is a closed list.
type searchView struct {
	Filters   filters
	Hits      []hit
	Projects  []string
	Statuses  []project.Status
	Types     []string
	Prios     []string
	Assignees []string
	Labels    []string
	Tags      []string
	// Menus is the filter bar: one per dimension, each option a link carrying
	// the rest of the bar. See finder.go.
	Menus []finderMenu
	// Cleared is where to go to drop every filter but the words.
	Cleared string
}

// StatusNames is the vocabulary the status menu offers.
func (v searchView) StatusNames() []string {
	names := make([]string, 0, len(v.Statuses))
	for _, s := range v.Statuses {
		names = append(names, s.Name)
	}
	return names
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	f := filters{
		Query:    strings.TrimSpace(r.FormValue("q")),
		Project:  r.FormValue("project"),
		Status:   r.FormValue("status"),
		Type:     r.FormValue("type"),
		Priority: r.FormValue("priority"),
		Assignee: r.FormValue("assignee"),
		Label:    r.FormValue("label"),
		Tag:      task.CleanTag(r.FormValue("tag")),
	}

	entries, err := s.entries(r)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	view := searchView{
		Filters:  f,
		Projects: c.ProjectKeys(),
		Statuses: c.Statuses,
		Types:    c.TypeNames(),
		Prios:    c.Priorities,
	}
	view.Assignees, view.Labels, view.Tags = vocabulary(entries)
	view.Menus = f.menus(view)
	view.Cleared = filters{Query: f.Query}.href()
	if !f.Empty() {
		view.Hits = s.search(f, entries)
	}

	s.render(w, r, "search.html", c, "Search", view)
}

// vocabulary is the assignees and labels the vault actually uses, sorted. They
// are not configured anywhere, so the only place to learn them is the tasks.
func vocabulary(entries []vault.Entry) (assignees, labels, tags []string) {
	seenWho, seenLabel, seenTag := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if who := e.Task.Assignee; who != "" && !seenWho[who] {
			seenWho[who] = true
			assignees = append(assignees, who)
		}
		for _, l := range e.Task.Labels {
			if !seenLabel[l] {
				seenLabel[l] = true
				labels = append(labels, l)
			}
		}
		// Every level of a nested tag is offered, so `area` narrows to
		// everything under it the way Obsidian's tag pane does.
		// One tag, whatever it was written as: Obsidian treats them as the
		// same and shows the casing it saw first, so the list offers the first
		// casing and matching ignores it.
		for _, t := range e.Task.Tags {
			for _, level := range task.TagTree(t) {
				folded := strings.ToLower(level)
				if !seenTag[folded] {
					seenTag[folded] = true
					tags = append(tags, level)
				}
			}
		}
	}
	sort.Strings(assignees)
	sort.Strings(labels)
	sort.Strings(tags)
	return assignees, labels, tags
}

// search is a plain substring scan over the vault. An index would be faster and
// would be one more thing that can disagree with the files; at the size a
// vault reaches, reading them is fast enough.
func (s *Server) search(f filters, entries []vault.Entry) []hit {
	needle := strings.ToLower(f.Query)
	var hits []hit

	for _, e := range entries {
		if e.Task == nil || !matches(f, e) {
			continue
		}
		text := e.Task.Title + "\n" + e.Task.Body()
		if needle != "" && !strings.Contains(strings.ToLower(text), needle) {
			continue
		}
		// The excerpt is drawn from the body, never from the title: the title
		// is the link directly above it, and a search that matched it would
		// otherwise print it twice. With nothing to point at — the words
		// matched the title, or there were no words — the description opens
		// instead, because acceptance criteria and comments are not a summary.
		context := excerpt(e.Task.Body(), needle)
		if context == "" {
			context = excerpt(e.Task.Description(), "")
		}
		hits = append(hits, hit{
			Href:    "/task/" + e.Key,
			Label:   e.Key + " " + e.Task.Title,
			Context: context,
			Task: &hitTask{
				Key: e.Key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
				Priority: e.Task.Priority, Assignee: e.Task.Assignee,
				Labels: e.Task.Labels, Tags: e.Task.Tags,
			},
		})
	}

	// A search narrowed by a task field is not a question about pages, and a
	// search with no text is a list of tasks rather than a scan of prose.
	if f.Narrowed() || needle == "" {
		return hits
	}

	for _, page := range s.pages() {
		full, err := s.abs(page + ".md")
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(full)
		if err != nil || !strings.Contains(strings.ToLower(string(raw)), needle) {
			continue
		}
		hits = append(hits, hit{
			Href: "/page/" + page, Label: page, Context: excerpt(string(raw), needle),
		})
	}
	return hits
}

func matches(f filters, e vault.Entry) bool {
	switch {
	case f.Project != "" && e.Project != f.Project:
		return false
	case f.Status != "" && e.Task.Status != f.Status:
		return false
	case f.Type != "" && e.Task.Type != f.Type:
		return false
	case f.Priority != "" && e.Task.Priority != f.Priority:
		return false
	case f.Assignee == unassigned && e.Task.Assignee != "":
		return false
	case f.Assignee != "" && f.Assignee != unassigned && e.Task.Assignee != f.Assignee:
		return false
	case f.Label != "" && !slices.Contains(e.Task.Labels, f.Label):
		return false
	case f.Tag != "" && !taggedWith(e.Task.Tags, f.Tag):
		return false
	}
	return true
}

// taggedWith matches a tag and everything nested under it: `area` finds
// `area/auth`, the way Obsidian's tag pane and its `tag:` search do. A tag is a
// hierarchy, and a filter that ignored the hierarchy would answer a different
// question from the one the same word answers in Obsidian.
//
// Case-insensitively, for the same reason: in Obsidian `#Auth` and `#auth` are
// one tag, shown under whichever casing was written first. Matching them as two
// would split a tag in half here that is whole there.
func taggedWith(tags []string, wanted string) bool {
	wanted = strings.ToLower(wanted)
	for _, t := range tags {
		t = strings.ToLower(t)
		if t == wanted || strings.HasPrefix(t, wanted+"/") {
			return true
		}
	}
	return false
}

// unassigned stands for the absence of an assignee, which a blank option cannot
// say: blank already means "any".
const unassigned = "!unassigned"

// excerpt is the words around the match. With no needle it is the opening of
// the text, which is what a result list wants when the search was a filter
// rather than a question.
func excerpt(text, needle string) string {
	if needle == "" {
		return clip(text, 0, 180)
	}
	at := strings.Index(strings.ToLower(text), needle)
	if at < 0 {
		return ""
	}
	return clip(text, at-60, at+len(needle)+60)
}

// clip takes the bytes between two offsets without cutting a character in half.
// A title in Cyrillic, Greek or Japanese is one byte-slice away from a row of
// replacement characters, and the whole point of the file naming is that a
// title may be written in any of them.
func clip(text string, from, to int) string {
	from = max(0, from)
	to = min(len(text), to)
	for from > 0 && from < len(text) && !utf8.RuneStart(text[from]) {
		from--
	}
	for to < len(text) && !utf8.RuneStart(text[to]) {
		to++
	}
	if from >= to {
		return ""
	}

	// A window cut at a fixed width starts and ends mid-word. Dropping the
	// partial word at each end costs a few characters and saves the reader
	// from "ession expires overnight", which reads as a typo rather than as an
	// excerpt. An ellipsis says the sentence goes on.
	fragment := plain(text[from:to])
	words := strings.Fields(fragment)
	if len(words) > 1 && from > 0 && !isBreak(rune(text[from-1])) {
		words = words[1:]
		if len(words) > 0 {
			words[0] = "…" + words[0]
		}
	}
	if len(words) > 1 && to < len(text) && !isBreak(rune(text[to])) {
		words = words[:len(words)-1]
		if len(words) > 0 {
			words[len(words)-1] += "…"
		}
	}
	return strings.Join(words, " ")
}

func isBreak(r rune) bool {
	return r == ' ' || r == '\n' || r == '\t' || r == '\r'
}

// plain takes the marks off a fragment of Markdown. An excerpt is one line of
// context, and `## Acceptance` or `- [ ]` in the middle of it is noise from a
// syntax that is not being rendered here.
var marks = strings.NewReplacer(
	"#", "", "**", "", "__", "", "`", "", ">", "",
	"[[", "", "]]", "",
	"- [ ]", "·", "- [x]", "·", "- ", "· ",
)

func plain(fragment string) string { return marks.Replace(fragment) }

// handleAssign changes who a task is on, from the page you are reading it on.
//
// The status could already be changed there and the assignee could not, which
// is an odd place to draw the line: they are the two things somebody changes
// while looking at a task rather than while editing one. Going to a separate
// form, finding the box, saving and coming back is four steps for a field with
// one value in it.
func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	who := strings.TrimSpace(r.FormValue("assignee"))
	// "Take it" is the same write with the handle filled in from the session,
	// rather than a second endpoint that could disagree with this one.
	if r.FormValue("me") == "1" {
		who = s.meAs(r)
	}

	author := s.authorFor(r)
	err := s.editTask(r, key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
		if t.Assignee == who {
			return "", nil, nil
		}
		was := t.Assignee

		// Somebody the host vouches for and the vault has never written down
		// gets a page, in this commit. The alternative is a link to a note that
		// does not exist — a ghost in the graph, and a backlinks pane with
		// nowhere to show the work.
		var also []string
		if who != "" {
			if member, ok := memberNamed(s.membersFor(r), who); ok {
				written, err := s.writePersonPage(who, member, s.hostKeyFor(r, who))
				if err != nil {
					return "", nil, err
				}
				if written != "" {
					also = append(also, written)
				}
			}
		}

		// A link when there is a page to link to, a plain handle when there is
		// not. A vault that keeps no people reads exactly as it did before any
		// of this, and no link is ever written to a note nobody wrote.
		if _, known := vault.PersonOf(s.peopleIn(), who); known {
			t.SetAssignee(who)
		} else {
			t.Set("assignee", who)
			t.Assignee = who
		}

		switch {
		case was == "":
			return key + ": assigned to " + who, also, nil
		case who == "":
			return key + ": unassigned, was " + was, also, nil
		}
		return key + ": " + was + " → " + who, also, nil
	})
	s.afterEdit(w, r, key, err)
}
