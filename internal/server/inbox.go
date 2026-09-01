package server

import (
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/reaction"
	"github.com/vadymdidenkolab/docket/internal/space"
)

// Letting the outside speak.
//
// A test run happens somewhere else — in CI, on somebody's machine, in a
// pipeline that finishes at three in the morning — and the result has to arrive
// without anybody opening a browser. That is the one direction the other three
// mechanisms do not cover: reactions listen to the vault, pages draw it,
// actions change it when asked.
//
// So an app can declare an address. What is posted to it goes to a program on
// stdin, exactly as an event does, and what the program writes is committed.
// The program is the app's; the decision to run it at all is the server's; and
// the secret that says who may post is the machine's, never the repository's.

// bodyLimit is how much may be posted. A JUnit file from a large suite is a few
// hundred kilobytes; ten megabytes is somebody's mistake or somebody's attack.
const bodyLimit = 10 << 20

// handleInbox takes what was posted and gives it to the program that asked for
// it.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	v, inbound, ok := s.inboxNamed(name)
	if !ok {
		http.Error(w, "no inbox called "+name, http.StatusNotFound)
		return
	}
	if !s.programs {
		http.Error(w, "this server was not started with --programs, so nothing ran",
			http.StatusServiceUnavailable)
		return
	}

	// Who may post. A named secret is checked against the server's own
	// environment; an inbox with none is only open on a server that has no
	// sign-in, which is the loopback case somebody set up deliberately.
	switch {
	case inbound.SecretEnv != "":
		wanted := strings.TrimSpace(os.Getenv(inbound.SecretEnv))
		if wanted == "" {
			http.Error(w, "this inbox names a secret in "+inbound.SecretEnv+
				" and this server has none", http.StatusServiceUnavailable)
			return
		}
		if !sameSecret(wanted, r.Header.Get("X-Docket-Secret")) {
			http.Error(w, "wrong or missing X-Docket-Secret", http.StatusForbidden)
			return
		}
	case s.auth != nil:
		http.Error(w, "this inbox names no secret, and this server signs people in. "+
			"Give it secret_env and set that variable where the server runs.",
			http.StatusForbidden)
		return
	}

	posted, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	before := dirtyIn(v.Root)
	said, runErr := reaction.Posted(r.Context(), v.Root, inbound.Run, posted, actionLife)
	after := dirtyIn(v.Root)

	var wrote []string
	for path := range after {
		if _, was := before[path]; !was {
			wrote = append(wrote, path)
		}
	}
	sort.Strings(wrote)

	if len(wrote) > 0 {
		message := name + ": " + plural(len(wrote), "file", "files") + " from outside"
		if err := v.Repo.Commit(wrote, message, s.author); err != nil {
			said += "\nWritten but not committed: " + err.Error()
		} else {
			s.after(r, v)
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if runErr != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		said = strings.TrimSpace(said + "\n" + runErr.Error())
	}
	if said == "" {
		said = "accepted"
	}
	_, _ = io.WriteString(w, said+"\n")
}

// inboxNamed finds a declared address and the repository that declared it.
func (s *Server) inboxNamed(name string) (*space.Vault, project.Inbound, bool) {
	for _, v := range s.sp().Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		for _, in := range c.Inbox {
			if strings.EqualFold(in.Name, name) {
				return v, in, true
			}
		}
	}
	return nil, project.Inbound{}, false
}

// sameSecret compares without leaking how much of it matched.
func sameSecret(wanted, given string) bool {
	if len(wanted) != len(given) {
		return false
	}
	same := byte(0)
	for i := range wanted {
		same |= wanted[i] ^ given[i]
	}
	return same == 0
}
