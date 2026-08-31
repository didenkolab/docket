package server

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/space"
)

// Sending what the board wrote to where everybody else will read it.
//
// Every write is a commit, and that used to be where it stopped: the commits
// sat in whichever clone the server happened to be running over. Somebody who
// took the project as a clone got everything except what the board did, which
// undoes the whole premise.
//
// Three choices, and the reasoning matters more than the code:
//
// **After every write, not on a button.** Forgetting to press it is exactly the
// failure being fixed, and a button somebody has to remember is a worse version
// of the current situation rather than a better one.
//
// **In the background, not in the request.** A card drag should not wait on the
// network, and a push that fails halfway through a request has nowhere useful
// to report itself.
//
// **Visible.** A background push that fails silently is the same bug again, so
// the interface says how many commits are waiting and what went wrong, and
// offers to try again. Nothing is hidden and nothing is retried forever.
//
// What is deliberately not done: recovering from somebody else having pushed
// first. That needs a rebase, and rebasing a working tree a human may have open
// in Obsidian is how work gets lost. It is reported, precisely, with the command
// to run — and a rebase is a decision somebody makes, not something a web
// server does behind them.

// pushing is what the board has been asked to send, per repository.
type pushing struct {
	mu sync.Mutex
	// state is the last outcome per repository prefix.
	state map[string]pushState
	// arrived is how many commits the remote had that this clone did not, as of
	// the last look. Kept apart from state because it is answered by a timer
	// rather than by a write.
	arrived map[string]int
	// running says a push for that repository is in flight, so a second write
	// does not start a second one.
	running map[string]bool
}

// pushState is what to say about one repository.
type pushState struct {
	// Waiting is how many commits are here and not on the remote.
	Waiting int
	// Trouble is why the last attempt failed, empty when it did not.
	Trouble string
	// Moved says the remote has moved on, which needs a person rather than a
	// retry.
	Moved bool
	// NoUpstream says the branch tracks nothing, so there is nowhere to push.
	NoUpstream bool
	// NoRemote says the repository has no remote at all, so nothing the board
	// writes will ever leave this folder.
	//
	// It used to say nothing whatsoever. A vault with no remote was skipped
	// before the panel was drawn, so the board committed, published nothing and
	// reported neither — which is the silence IGL-37 was written against, only
	// one step earlier in the chain. Reported once the board has actually
	// written something, because a local-only vault nobody has touched is a
	// normal thing to have and not something to nag about.
	NoRemote bool
	// Wrote is how many changes the board has made here this run.
	Wrote int

	// Waiting the other way: how many commits the remote has that this clone
	// does not.
	//
	// The board pushed and never looked at what arrived. A vault is a
	// repository, so the other ways in are ordinary — Obsidian on another
	// machine, a merged proposal, an agent in a clone — and the board went on
	// drawing a board that was out of date with nothing to say so.
	Arrived int
	// Diverged says both sides have moved, so taking what arrived is a merge
	// and a person makes it.
	Diverged bool
	// Unclean says somebody has uncommitted changes in the folder, which a
	// fast-forward would overwrite.
	Unclean bool
}

// Settled reports whether there is nothing to say.
func (p pushState) Settled() bool {
	return p.Waiting == 0 && p.Arrived == 0 && p.Trouble == "" && !p.NoUpstream && !p.NoRemote
}

func newPushing() *pushing {
	return &pushing{state: map[string]pushState{}, running: map[string]bool{},
		arrived: map[string]int{}}
}

// after sends what was just committed, in the background.
//
// The credential is taken from the request rather than from anywhere durable:
// the token belongs to the person who made the change, and pushing as them is
// what makes the remote's history say who did what. Without a session it is
// empty, and git falls back to the machine's own credential helper — which is
// the right answer for a board somebody runs over their own clone.
func (s *Server) after(r *http.Request, v *space.Vault) {
	if s.pushes == nil || v == nil || v.Repo == nil {
		return
	}
	if !v.Repo.HasRemote() {
		// Nowhere to send it, which is a fact worth saying rather than a
		// reason to say nothing.
		s.pushes.mu.Lock()
		state := s.pushes.state[v.Prefix]
		state.NoRemote = true
		state.Wrote++
		s.pushes.state[v.Prefix] = state
		s.pushes.mu.Unlock()
		return
	}

	cred := s.credentialFor(r, v)
	prefix := v.Prefix

	s.pushes.mu.Lock()
	if s.pushes.running[prefix] {
		// One at a time, and nothing is lost by returning: the push in flight
		// asks git what is left when it finishes, and git knows about this
		// commit whether or not anybody told it.
		s.pushes.mu.Unlock()
		return
	}
	s.pushes.running[prefix] = true
	s.pushes.mu.Unlock()

	go func() {
		// Push, then ask what is left, and if anything is, push that too.
		//
		// The first version of this remembered that a write had arrived while
		// the push was in the air, and went round again for it. That was a
		// second record of a fact git already holds — and a worse one, because
		// it only knew about writes the board made. A commit an agent wrote in
		// the folder, or `docket new` on the command line, was invisible to it.
		//
		// The count is the truth. Asking it after every push covers every way a
		// commit can appear, which is the whole reason this project keeps no
		// state beside the repository.
		// Push, then ask what is left, and if anything is, push that too.
		//
		// The count is read while holding the same lock that says whether a
		// push is running, and that is the whole of the correctness here. A
		// write commits and then calls after, which takes the lock; if it finds
		// a push in flight it returns, trusting this loop to notice. Reading
		// the count outside the lock left a window where it did not: count zero,
		// commit lands, write returns, loop clears the flag, commit stranded.
		// One flaky test in ten runs, which is the worst kind.
		//
		// An earlier version kept a flag saying "a write arrived" instead. That
		// was a second record of a fact git already holds, and a narrower one:
		// it knew nothing of a commit an agent made in the folder or one from
		// `docket new`. The count knows about every commit however it got there.
		previous := -1
		for {
			err := v.Repo.Push(cred)

			s.pushes.mu.Lock()
			waiting, countErr := v.Repo.Unpushed()

			state := pushState{Waiting: waiting}
			switch {
			case errors.Is(countErr, gitvcs.ErrNoUpstream):
				state.NoUpstream = true
			case err != nil && errors.Is(err, gitvcs.ErrNotFastForward):
				state.Trouble, state.Moved = err.Error(), true
			case err != nil:
				state.Trouble = err.Error()
			}
			s.pushes.state[prefix] = state

			// Round again while something is still waiting and the last attempt
			// worked. Not when it failed: pushing at a host that has just
			// refused is how a server hammers one, and the retry is a button.
			//
			// And not when the count has stopped falling. A push that reports
			// success while leaving the same commits behind is a state nobody
			// has seen, and a loop that trusted it would spin against a host
			// forever.
			if state.Trouble == "" && countErr == nil && waiting > 0 &&
				(previous < 0 || waiting < previous) {
				previous = waiting
				s.pushes.mu.Unlock()
				continue
			}
			s.pushes.running[prefix] = false
			s.pushes.mu.Unlock()
			return
		}
	}()
}

// credentialFor is what to prove a push with: the signed-in person's token for
// that repository's host, or nothing, which leaves it to git.
func (s *Server) credentialFor(r *http.Request, v *space.Vault) gitvcs.Credential {
	if s.auth == nil {
		return gitvcs.Credential{}
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return gitvcs.Credential{}
	}
	current, ok := s.auth.lookup(cookie.Value)
	if !ok {
		return gitvcs.Credential{}
	}
	for _, repo := range s.auth.repositories() {
		if repo.prefix != v.Prefix || repo.host == nil {
			continue
		}
		if token, held := current.tokenFor(repo.hostKey); held {
			return gitvcs.Credential{User: repo.host.GitUser(), Token: token}
		}
	}
	return gitvcs.Credential{}
}

/* ---------- what to say about it ---------- */

// pushNote is one repository's standing, for the interface.
type pushNote struct {
	Name string
	pushState
}

// pushNotes is every repository with something to say. A repository in step
// says nothing, because a badge that is always there is a badge nobody reads.
func (s *Server) pushNotes() []pushNote {
	if s.pushes == nil {
		return nil
	}
	s.pushes.mu.Lock()
	defer s.pushes.mu.Unlock()

	var notes []pushNote
	for _, v := range s.sp().Vaults() {
		if v.Repo == nil {
			continue
		}
		if !v.Repo.HasRemote() {
			// Only once it has written something. Until then there is nothing
			// stranded, and a permanent badge is a badge nobody reads.
			if state, wrote := s.pushes.state[v.Prefix]; wrote && state.NoRemote {
				notes = append(notes, pushNote{Name: s.nameOf(v), pushState: state})
			}
			continue
		}
		state, known := s.pushes.state[v.Prefix]
		if !known {
			// Nothing has been pushed this run, but there may be commits from
			// before it — from an agent, or from a previous run.
			waiting, err := v.Repo.Unpushed()
			switch {
			case errors.Is(err, gitvcs.ErrNoUpstream):
				state = pushState{NoUpstream: true}
			case err == nil:
				state = pushState{Waiting: waiting}
			}
		}
		// What arrived is read from the last look rather than fetched here: a
		// page load must not wait on the network, and looking is what the timer
		// in watchRemote is for.
		state.Arrived = s.pushes.arrived[v.Prefix]
		if state.Settled() {
			continue
		}
		notes = append(notes, pushNote{Name: s.nameOf(v), pushState: state})
	}
	return notes
}

// handlePush is the retry: somebody read what went wrong and is asking again.
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	for _, v := range s.sp().Vaults() {
		if v.Repo == nil || !v.Repo.HasRemote() {
			continue
		}
		if !s.mayWriteTo(r, v) {
			continue
		}
		s.after(r, v)
	}
	http.Redirect(w, r, backTo(r.FormValue("next")), http.StatusSeeOther)
}

// mayWriteTo reports whether the person asking may change this repository —
// which is who may push it, because a push publishes what they wrote.
func (s *Server) mayWriteTo(r *http.Request, v *space.Vault) bool {
	st := standingIn(r)
	if st == nil {
		return true
	}
	for _, repo := range s.auth.repositories() {
		if repo.prefix != v.Prefix {
			continue
		}
		for _, key := range repo.projects {
			if st.CanWrite(key) {
				return true
			}
		}
		return false
	}
	return false
}

/* ---------- what arrived ---------- */

// watchRemote looks, on a timer, for commits this clone has not got.
//
// A fetch is read-only and moves nothing in the working tree, so it is safe to
// do without anybody asking. Taking what it finds is not, and is a button.
//
// On a timer rather than on a page load, because a board that waited on the
// network to draw itself would be a board that hangs when the network does.
func (s *Server) watchRemote(every time.Duration) {
	if every <= 0 {
		return
	}
	every = watchEvery(every)
	look := func() {
		for _, v := range s.sp().Vaults() {
			if v.Repo == nil || !v.Repo.HasRemote() {
				continue
			}
			// No credential: this runs with nobody's session, so it can only
			// look at what the machine's own git can reach. A private remote
			// that needs a token reports nothing here rather than failing
			// loudly every minute at somebody who is not even reading.
			arrived, err := v.Repo.Waiting(gitvcs.Credential{})
			if err != nil {
				continue
			}
			s.pushes.mu.Lock()
			s.pushes.arrived[v.Prefix] = arrived
			s.pushes.mu.Unlock()
		}
	}

	go func() {
		look()
		for range time.Tick(every) {
			look()
		}
	}()
}

// watchEvery is how often to look, never faster than makes sense.
//
// The watcher was given the same interval as the access re-check, which a test
// sets to a nanosecond so a revoked token is noticed immediately. That is the
// right value for asking a host about a person and a ruinous one here: every
// tick runs `git fetch` over every vault, so the suite spawned git as fast as
// the scheduler allowed. It finished on this laptop and hung a release build
// for twenty minutes on two cores.
//
// A remote that moved a second ago and one that moved half a minute ago are the
// same news to somebody reading a board. There is nothing to gain by asking
// more often, and a process to spawn every time.
func watchEvery(asked time.Duration) time.Duration {
	const floor = 30 * time.Second
	if asked < floor {
		return floor
	}
	return asked
}

// handleTake brings in what the remote has.
//
// Fast-forward only. A merge of two plans is a decision, and this one would be
// made against a working tree somebody may have open in Obsidian — the same
// reasoning that stops the board rebasing when a push is refused.
func (s *Server) handleTake(w http.ResponseWriter, r *http.Request) {
	var trouble string
	took := 0

	for _, v := range s.sp().Vaults() {
		if v.Repo == nil || !v.Repo.HasRemote() || !s.mayWriteTo(r, v) {
			continue
		}
		arrived, err := v.Repo.Take(s.credentialFor(r, v))
		switch {
		case errors.Is(err, gitvcs.ErrWouldDiverge):
			s.pushes.mu.Lock()
			state := s.pushes.state[v.Prefix]
			state.Diverged = true
			s.pushes.state[v.Prefix] = state
			s.pushes.mu.Unlock()
			trouble = s.nameOf(v) + " has commits the remote does not, and the remote has " +
				plural(arrived, "commit", "commits") + " this clone does not. " +
				"Joining two plans is a decision: do it in the folder, with git."
		case errors.Is(err, gitvcs.ErrNotClean):
			trouble = "There are uncommitted changes in " + s.nameOf(v) +
				". A fast-forward would write over them — commit them, or put them aside, first."
		case err != nil:
			trouble = "Could not take what arrived in " + s.nameOf(v) + ": " + err.Error()
		default:
			took += arrived
			s.pushes.mu.Lock()
			s.pushes.arrived[v.Prefix] = 0
			s.pushes.mu.Unlock()
		}
	}

	if took > 0 {
		// The files on disk have changed under the board, so it has to read
		// them again — the vocabulary may have moved too.
		if err := s.reload(); err != nil {
			trouble = "Took what arrived, and then could not read the vault: " + err.Error()
		}
	}
	if trouble != "" {
		s.fail(w, r, http.StatusConflict, "Cannot take what arrived", trouble)
		return
	}
	http.Redirect(w, r, backTo(r.FormValue("next")), http.StatusSeeOther)
}
