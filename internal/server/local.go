package server

import (
	"net/http"

	"github.com/vadymdidenkolab/docket/internal/access"
)

// Signing in with what is already on this machine.
//
// The board is usually run on a laptop over a repository that was cloned from
// the host it is asking about — so the credential is already there, in the
// keychain, and git uses it on every push. Asking somebody to fetch it again is
// asking them to look up something they have.
//
// This is authentication by "you can reach this port", so it is offered under
// exactly two conditions, both necessary:
//
//   - the listener is on loopback, so reaching the port means being on this
//     machine;
//   - nothing is proxying to it, because a reverse proxy binds loopback and is
//     reachable from the world, and then "you can reach this port" would mean
//     anybody at all.
//
// The second is why it cannot be inferred from the address alone. Behind a
// proxy this must be off, and the server is told rather than guessing.

// localSignIn reports whether the machine's own credentials may be used, and
// for which host.
//
// One host only. With several it would be ambiguous which one a button meant,
// and the honest thing is to say so — but that case does not arise on a laptop
// serving one repository, which is the case this exists for.
func (s *Server) localSignIn() (signInHost, bool) {
	if s.auth == nil || !s.onLoopback {
		return signInHost{}, false
	}
	return s.auth.oneHost()
}

// handleLocalSignIn takes the token git would use and opens a session with it.
func (s *Server) handleLocalSignIn(w http.ResponseWriter, r *http.Request) {
	host, ok := s.localSignIn()
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	next := backTo(r.FormValue("next"))

	fail := func(message string) {
		c, _ := s.config()
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "sign-in.html", c, "Sign in", s.signInPage(r, next, message))
	}

	token, err := access.LocalToken(r.Context(), host.Key)
	if err != nil {
		fail("This machine has no credentials for " + host.Name + ": " + err.Error() +
			". Sign in there with git or gh once, or paste a token below.")
		return
	}
	// The token is the machine's; whose it is, is still the host's answer.
	if err := s.auth.verify(r.Context(), host.Key, token); err != nil {
		fail("The credentials on this machine were refused: " + err.Error())
		return
	}
	s.establish(w, r, host.Key, token, next, fail)
}
