package server

import (
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/reaction"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Running what the vault asked to be run.
//
// The declarations are in docket.yaml — shared, reviewed, the same for everyone.
// Whether they run is not: this server runs nothing unless it was started with
// --reactions. Push access to a repository is not permission to execute code on
// the machine serving it, which is the same reason git does not put hooks in
// the repository. See internal/reaction.
//
// What a reaction writes is committed separately, and only what it wrote: the
// files already dirty before it ran are somebody editing in the folder, and
// sweeping those into a commit nobody asked for is how a board loses work.

// react runs the reactions this vault declared for an event, and commits what
// they wrote.
//
// Failures are logged and not returned. The change that caused the event is
// already committed; refusing the request afterwards would say the move did not
// happen when it did.
func (s *Server) react(r *http.Request, v *space.Vault, e reaction.Event) {
	if v == nil || v.Repo == nil {
		return
	}
	c, err := project.Load(v.Root)
	if err != nil || len(c.Reactions) == 0 {
		return
	}

	declared := make([]reaction.Declared, 0, len(c.Reactions))
	for _, d := range c.Reactions {
		declared = append(declared, reaction.Declared(d))
	}

	if !s.reactions {
		// Said once per event rather than silently: a vault whose automation is
		// not running should be able to find out why without reading the source.
		log.Printf("docket: %s declares %d reactions and this server was not started "+
			"with --reactions, so none ran", v.Prefix, len(declared))
		return
	}

	before := dirtyIn(v.Root)
	results := reaction.Run(r.Context(), v.Root, declared, e)
	if len(results) == 0 {
		return
	}
	after := dirtyIn(v.Root)

	var names []string
	for _, result := range results {
		if result.Err != nil {
			log.Printf("docket: reaction %s: %v — %s", result.Name, result.Err, result.Output)
			continue
		}
		names = append(names, result.Name)
	}

	// Only what they wrote. A file that was already dirty is somebody's
	// unfinished edit in the folder, and it is not this commit's business.
	var wrote []string
	for path := range after {
		if _, was := before[path]; !was {
			wrote = append(wrote, path)
		}
	}
	if len(wrote) == 0 || len(names) == 0 {
		return
	}
	sort.Strings(wrote)

	author := gitvcs.Author{
		Name:  s.authorFor(r).Name,
		Email: s.authorFor(r).Email,
	}
	message := "reaction " + strings.Join(names, ", ") + ": after " + e.Event
	if e.Key != "" {
		message += " " + e.Key
	}
	if err := v.Repo.Commit(wrote, message, author); err != nil {
		log.Printf("docket: a reaction wrote %v and it could not be committed: %v", wrote, err)
		return
	}
	s.after(r, v)
}

// dirtyIn is what git says is uncommitted, by path relative to the vault.
func dirtyIn(root string) map[string]struct{} {
	out := map[string]struct{}{}
	cmd := exec.Command("git", "status", "--porcelain", "-z")
	cmd.Dir = root
	said, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, entry := range strings.Split(string(said), "\x00") {
		if len(entry) < 4 {
			continue
		}
		// "XY path", and for a rename the second half arrives as its own entry.
		out[entry[3:]] = struct{}{}
	}
	return out
}

// movedEvent is what a reaction is told about a card that changed column.
func (s *Server) movedEvent(r *http.Request, key, path, title, from, to, projectKey, root string) reaction.Event {
	return reaction.Event{
		Event: reaction.OnMoved, Key: key, Project: projectKey, Path: path, Title: title,
		From: from, To: to, Who: s.authorFor(r).Name, When: s.now().UTC().Format(task.TimeFormat),
		Root: root,
	}
}
