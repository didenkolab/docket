// Package mcp serves a vault over the Model Context Protocol, so an agent can
// work through a contract rather than by guessing at files.
//
// It is not a replacement for the files. An agent that would rather read
// ACME/ACME-12 … .md directly is welcome to; this exists because an agent
// driving a tracker wants "move this to In review" to be one call that
// validates, writes and commits, not four file operations that might each be
// half-right.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Protocol is the revision this server speaks.
const Protocol = "2024-11-05"

// Server answers MCP calls against one vault.
type Server struct {
	// Space is what the agent was pointed at: a vault, or a workspace of them.
	// A task is found by key across all of them, and a write goes to the
	// repository that owns it.
	Space  *space.Space
	Author gitvcs.Author
	Now    func() time.Time

	writes sync.Mutex
}

// New opens a vault, or a workspace of them, for an agent.
func New(root string, author gitvcs.Author) (*Server, error) {
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
	return &Server{Space: sp, Author: author, Now: time.Now}, nil
}

// Root is where the space is, for anything that has to say so.
func (s *Server) Root() string { return s.Space.Root }

/* ---------- JSON-RPC ---------- */

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests from in and writes answers to out, one JSON object per
// line, until in is exhausted.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	encoder := json.NewEncoder(out)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = encoder.Encode(response{JSONRPC: "2.0",
				Error: &rpcError{Code: -32700, Message: "cannot parse: " + err.Error()}})
			continue
		}

		result, err := s.dispatch(req)
		// A notification has no id and wants no answer.
		if req.ID == nil {
			continue
		}
		answer := response{JSONRPC: "2.0", ID: req.ID}
		if err != nil {
			answer.Error = &rpcError{Code: -32000, Message: err.Error()}
		} else {
			answer.Result = result
		}
		if err := encoder.Encode(answer); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) dispatch(req request) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": Protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "docket", "version": "1"},
		}, nil

	case "notifications/initialized", "ping":
		return map[string]any{}, nil

	case "tools/list":
		return map[string]any{"tools": tools}, nil

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
		return s.call(params.Name, params.Arguments)

	default:
		return nil, fmt.Errorf("unknown method %q", req.Method)
	}
}

// text wraps a result the way MCP expects a tool answer to look.
func text(format string, args ...any) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf(format, args...)}},
	}
}

// textResult is the same thing shaped for a tool handler, which returns a
// result and an error.
func textResult(format string, args ...any) (any, error) { return text(format, args...), nil }

// jsonText answers with JSON in the text field: an agent reads a list better as
// data than as prose, and MCP has nowhere else to put it.
func jsonText(v any) (any, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return text("%s", raw), nil
}

/* ---------- the tools ---------- */

var tools = []map[string]any{
	{
		"name":        "list_tasks",
		"description": "List tasks in the vault, optionally narrowed by project, status, status category or assignee.",
		"inputSchema": object(map[string]any{
			"project":  str("Project key, such as ACME"),
			"status":   str("Exact status name"),
			"category": str("todo, doing or done"),
			"assignee": str("Whose tasks, such as agent/claude"),
		}, nil),
	},
	{
		"name":        "get_task",
		"description": "Read one task, with its body, comments and the version needed to write it back.",
		"inputSchema": object(map[string]any{"key": str("Task key, such as ACME-12")}, []string{"key"}),
	},
	{
		"name":        "create_task",
		"description": "Create a task. The key is allocated from the project's highest number.",
		"inputSchema": object(map[string]any{
			"title":       str("What the task is, in one line"),
			"project":     str("Project key; the vault's first if omitted"),
			"type":        str("Task type from the vault's vocabulary"),
			"priority":    str("Priority from the vault's vocabulary"),
			"assignee":    str("Who it is on"),
			"parent":      str("Key of the parent task"),
			"description": str("Markdown body"),
			"labels":      list("Labels"),
			"estimate":    number("How big it is, in the unit docket.yaml declares; must be on its scale"),
			"sprint":      str("Title of a sprint page to put it in"),
		}, []string{"title"}),
	},
	{
		"name": "update_task",
		"description": "Change a task. Pass the version from get_task to be refused rather than " +
			"overwrite a change made in Obsidian or by someone else.",
		"inputSchema": object(map[string]any{
			"key":         str("Task key"),
			"version":     str("The version from get_task"),
			"title":       str("New title; renames the file"),
			"status":      str("New status; must be allowed by the workflow"),
			"priority":    str("New priority"),
			"assignee":    str("New assignee; empty string unassigns"),
			"description": str("New Markdown body, replacing the old one"),
			"comment":     str("Text to append as a comment"),
			"labels":      list("Labels, replacing the old ones"),
			"estimate":    number("How big it is, in the unit docket.yaml declares; must be on its scale. Not offered for a task with children — a container's size is what they add up to"),
			"sprint":      str("Title of a sprint page, or an empty string to take it out of every sprint"),
		}, []string{"key"}),
	},
	{
		"name":        "search",
		"description": "Search tasks and pages for a piece of text.",
		"inputSchema": object(map[string]any{"query": str("What to look for")}, []string{"query"}),
	},
	{
		"name":        "read_page",
		"description": "Read a page from the knowledge base.",
		"inputSchema": object(map[string]any{"path": str("Path under docs/, without .md")}, []string{"path"}),
	},
	{
		"name":        "write_page",
		"description": "Write a page in the knowledge base, creating it if needed.",
		"inputSchema": object(map[string]any{
			"path":  str("Path under docs/, without .md"),
			"title": str("Page title"),
			"body":  str("Markdown body"),
		}, []string{"path", "title", "body"}),
	},
	{
		"name":        "check",
		"description": "Validate the vault against the format's rules and report every finding.",
		"inputSchema": object(map[string]any{}, nil),
	},
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// number is a schema for an amount. Not an integer: a vault counting days wants
// halves, and a scale is a list of numbers rather than of whole ones.
func number(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func list(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": description}
}

func object(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (s *Server) call(name string, raw json.RawMessage) (any, error) {
	switch name {
	case "list_tasks":
		return s.listTasks(raw)
	case "get_task":
		return s.getTask(raw)
	case "create_task":
		return s.createTask(raw)
	case "update_task":
		return s.updateTask(raw)
	case "search":
		return s.search(raw)
	case "read_page":
		return s.readPage(raw)
	case "write_page":
		return s.writePage(raw)
	case "check":
		return s.check()
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

type taskView struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Category string   `json:"category"`
	Priority string   `json:"priority"`
	Assignee string   `json:"assignee,omitempty"`
	Parent   string   `json:"parent,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// Relations is how this task says it is connected to others, keyed by the
	// verb: blocks, blocked_by, relates.
	Relations map[string][]string `json:"relations,omitempty"`
	Path      string              `json:"path"`
	Note      string              `json:"note"` // what a wikilink to this task says
}

func view(e vault.Entry, relations []string) taskView {
	return taskView{
		Key: e.Key, Title: e.Task.Title, Type: e.Task.Type,
		Status: e.Task.Status, Category: e.Task.StatusCategory,
		Priority: e.Task.Priority, Assignee: e.Task.Assignee, Parent: e.Task.Parent,
		Labels: e.Task.Labels, Tags: e.Task.Tags, Relations: e.Task.AllRelations(relations),
		Path: e.Path, Note: e.Note(),
	}
}

func (s *Server) listTasks(raw json.RawMessage) (any, error) {
	var args struct {
		Project, Status, Category, Assignee string
	}
	_ = json.Unmarshal(raw, &args)

	entries, err := s.Space.Entries()
	if err != nil {
		return nil, err
	}
	// Which verbs a task can carry is the vault's vocabulary. A space whose
	// configuration cannot be read still lists its tasks: the relations then
	// fall back to the ones the format ships.
	c, _ := s.Space.Config()

	out := []taskView{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if args.Project != "" && e.Project != args.Project {
			continue
		}
		if args.Status != "" && e.Task.Status != args.Status {
			continue
		}
		if args.Category != "" && e.Task.StatusCategory != args.Category {
			continue
		}
		if args.Assignee != "" && e.Task.Assignee != args.Assignee {
			continue
		}
		out = append(out, view(e, relationFields(c)))
	}
	return jsonText(out)
}

func (s *Server) getTask(raw json.RawMessage) (any, error) {
	var args struct{ Key string }
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}

	if _, _, _, err := s.Space.Locate(args.Key); err != nil {
		return nil, err
	}
	c, err := s.Space.Config()
	if err != nil {
		return nil, err
	}
	entries, err := s.Space.Entries()
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if e.Key != args.Key || e.Task == nil {
			continue
		}
		return jsonText(struct {
			taskView
			Version     string         `json:"version"`
			Description string         `json:"description"`
			Comments    []task.Comment `json:"comments,omitempty"`
			Reachable   []string       `json:"reachable_statuses"`
		}{
			taskView:    view(e, relationFields(c)),
			Version:     fingerprint(e.Raw),
			Description: e.Task.Description(),
			Comments:    e.Task.Comments(),
			Reachable:   statusNames(c.Reachable(e.Task.Status)),
		})
	}
	return nil, fmt.Errorf("%s is in this space but could not be read", args.Key)
}

func statusNames(statuses []project.Status) []string {
	out := make([]string, len(statuses))
	for i, s := range statuses {
		out[i] = s.Name
	}
	return out
}

func (s *Server) createTask(raw json.RawMessage) (any, error) {
	var args struct {
		Project, Title, Type, Priority, Assignee, Parent, Description string
		Labels, Tags                                                  []string
		Estimate                                                      *float64
		Sprint                                                        string
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}

	c, err := s.Space.Config()
	if err != nil {
		return nil, err
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	projectKey := args.Project
	if projectKey == "" {
		if keys := c.ProjectKeys(); len(keys) > 0 {
			projectKey = keys[0]
		}
	}
	// A project lives in exactly one repository, and that is where its tasks go.
	own, v, err := s.Space.ConfigOf(projectKey)
	if err != nil {
		return nil, err
	}

	inVault, t, err := vault.Create(v.Root, own, vault.NewOptions{
		Project: projectKey, Title: args.Title, Type: args.Type,
		Priority: args.Priority, Assignee: args.Assignee, Parent: args.Parent,
		Description: args.Description, Labels: args.Labels, Tags: args.Tags,
		Estimate: args.Estimate, Sprint: sprintNote(s.Space, args.Sprint), Now: s.Now(),
	})
	if err != nil {
		return nil, err
	}
	if err := v.Repo.Commit([]string{inVault}, t.Key+": "+t.Title, s.Author); err != nil {
		return nil, err
	}
	rel := v.PathIn(inVault)
	return textResult("Created %s at %s. Link to it with [[%s]].",
		t.Key, rel, strings.TrimSuffix(relBase(rel), ".md"))
}

// sprintNote resolves a sprint by title to the note a link has to name, or
// leaves it as given so vault.Create writes what was asked and `docket check`
// reports a sprint page that does not exist.
func sprintNote(sp *space.Space, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	for _, found := range sp.Sprints() {
		if strings.EqualFold(found.Note, title) {
			return found.Note
		}
	}
	return title
}

func relBase(rel string) string {
	if slash := strings.LastIndex(rel, "/"); slash >= 0 {
		return rel[slash+1:]
	}
	return rel
}

// relationFields is the vault's relations, as property names. Which verbs exist
// is vocabulary, so it comes from the configuration rather than from a list in
// the code — see project.Relations.
func relationFields(c *project.Config) []string {
	if c == nil {
		c = &project.Config{}
	}
	var names []string
	for _, r := range c.Relations() {
		names = append(names, r.Name)
	}
	return names
}
