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
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string,
	c *project.Config, title string, data any) {

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := pageData{
		Config: c, Title: title, Data: data,
		You:      identityOf(r),
		SignedIn: s.auth != nil,
		CSRF:     tokenOf(r),
	}
	if err := s.tmpl.ExecuteTemplate(w, name, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	// order is where somebody put this card in its column, if anybody has.
	// Cards without one follow the ones with, in key order.
	order *int
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
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := s.entries()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	selected := r.URL.Query().Get("project")
	if selected != "" && !c.HasProject(selected) {
		s.fail(w, r, http.StatusNotFound, "No such project",
			selected+" is not in this vault: it holds "+strings.Join(c.ProjectKeys(), ", "))
		return
	}

	view := boardView{Selected: selected}
	for _, status := range c.Statuses {
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
		own, _, err := s.space.ConfigOf(projectKey)
		if err != nil {
			own = c
		}
		vocabulary[projectKey] = own
		return own
	}

	counts := map[string]int{}
	for _, e := range entries {
		if e.Err != nil {
			view.Broken = append(view.Broken, e)
			continue
		}
		counts[e.Project]++
		if selected != "" && e.Project != selected {
			continue
		}
		for i := range view.Columns {
			if view.Columns[i].Status.Name == e.Task.Status {
				view.Columns[i].Cards = append(view.Columns[i].Cards, card{
					Key: e.Key, Href: "/task/" + e.Key, Project: e.Project,
					Title: e.Task.Title, Status: e.Task.Status,
					Assignee: e.Task.Assignee, Priority: e.Task.Priority,
					Labels: e.Task.Labels, Tags: e.Task.Tags, Version: version(e.Raw),
					Reachable: reachableList(configOf(e.Project), e.Task.Status),
					order:     e.Task.Order,
				})
				view.Total++
			}
		}
	}

	for i := range view.Columns {
		sortCards(view.Columns[i].Cards)
	}

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

	s.render(w, r, "task.html", c, t.Key+" "+t.Title, taskView{
		Task:        t,
		Reachable:   c.Reachable(t.Status),
		Project:     projectKey,
		ProjectName: c.ProjectName(projectKey),
		Description: renderMarkdown(t.Description(), ix),
		Comments:    renderComments(t.Comments(), ix),
		Children:    s.childrenOf(c, key),
		Backlinks:   s.backlinks(strings.TrimSuffix(path.Base(rel), ".md"), rel),
		Version:     ver,
		Path:        rel,
	})
}

type taskView struct {
	Task        *task.Task
	Reachable   []project.Status
	Project     string
	ProjectName string
	Description template.HTML
	Comments    []renderedComment
	Children    []childTask
	Backlinks   []mention
	Version     string
	Path        string
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
func (s *Server) childrenOf(c *project.Config, key string) []childTask {
	entries, err := s.entries()
	if err != nil {
		return nil
	}
	var children []childTask
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == key {
			children = append(children, childTask{
				Key: e.Key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
			})
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
	err = s.editTask(key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
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
	err := s.editTask(key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
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
		err = s.commit([]string{rel}, t.Key+": "+t.Title, author)
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

	paths := s.pages()

	s.render(w, r, "pages.html", c, "Pages", paths)
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

	entries, err := s.entries()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	view := searchView{
		Filters:  f,
		Projects: c.ProjectKeys(),
		Statuses: c.Statuses,
		Types:    c.Types,
		Prios:    c.Priorities,
	}
	view.Assignees, view.Labels, view.Tags = vocabulary(entries)
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
