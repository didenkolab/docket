package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/space"
)

// Reviewing a proposal you were sent a link to.
//
// The change page reads a branch, and a branch is what a proposal is. But
// nobody is sent a branch: they are sent a pull request address, in a message
// or a notification, and the branch name is somewhere inside that page. Asking
// them to go and find it is asking them to do by hand the one lookup the host
// answers in a single call.
//
// It also brings in the proposals that are only on the remote. A branch pushed
// by somebody else is not in this clone until it is fetched, so the branch list
// used to show your own proposals and none of the ones you were being asked
// about — exactly backwards for reviewing.

// handleReview takes a pull request address and goes to what it would do.
//
// Nothing is written and nothing is checked out: the ref is fetched, which is
// the least that lets it be read, and then the ordinary change page does the
// rest.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.FormValue("url"))

	// A branch name typed in the box is a branch name. Somebody who already
	// knows it should not have to go and find a URL for it.
	looksLikeALink := strings.Contains(raw, "/pull") || strings.Contains(raw, "/merge_requests")
	if raw != "" && !looksLikeALink {
		if s.known(raw) {
			http.Redirect(w, r, "/change/"+url.PathEscape(raw), http.StatusSeeOther)
			return
		}
		// Refused as what it is. Reading it on as a URL produced "that is on
		// my-branch, and this repository is on github.com", which is an answer
		// to a question nobody asked.
		s.fail(w, r, http.StatusBadRequest, "No such branch",
			"“"+raw+"” is not a branch of this repository, and it is not a "+
				"pull request address either.")
		return
	}

	v, host, ok := s.pullRequestHost()
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "Nowhere to ask",
			"This repository has no host that answers questions about pull requests, "+
				"so a branch has to be named directly.")
		return
	}

	number, err := access.PullRequestNumber(host, raw)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cannot read that address", capitalise(err.Error()))
		return
	}

	token, held := s.tokenFor(r, host.HostName())
	if !held {
		s.fail(w, r, http.StatusForbidden, "Not signed in to "+host.Name(),
			"Asking "+host.Name()+" which branch a "+host.PullRequestName()+
				" is from needs a sign-in. Sign in, then paste the address again.")
		return
	}

	ref, err := host.PullRequest(r.Context(), token, number)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway,
			host.Name()+" did not answer", capitalise(err.Error()))
		return
	}

	// The branch is usually not in this clone — that is the whole reason for
	// this page — so bring it down before reading it. Fetching a single ref
	// touches nothing in the working tree.
	if !s.known(ref) && v.Repo != nil {
		if err := v.Repo.Fetch(s.credentialFor(r, v), ref); err != nil {
			s.fail(w, r, http.StatusBadGateway, "Cannot fetch "+ref,
				host.PullRequestName()+" "+number+" is from "+ref+
					", and this clone could not get it: "+err.Error())
			return
		}
	}

	// Read it under the name git now knows it by. A fetched ref that has no
	// local branch is origin/<ref>.
	at := ref
	if !s.known(at) {
		at = "origin/" + ref
	}
	if !s.known(at) {
		s.fail(w, r, http.StatusNotFound, "No such branch",
			host.PullRequestName()+" "+number+" says it is from "+ref+
				", which is not a branch of this repository.")
		return
	}
	http.Redirect(w, r, "/change/"+url.PathEscape(at), http.StatusSeeOther)
}

// pullRequestHost is the repository holding the board and the host that can be
// asked about its pull requests.
//
// The first vault, because that is the one the branch pages read; a workspace
// reviews a proposal to the repository it is a board of.
func (s *Server) pullRequestHost() (*space.Vault, access.PullRequestHost, bool) {
	vaults := s.sp().Vaults()
	if len(vaults) == 0 || vaults[0].Repo == nil || s.auth == nil {
		return nil, nil, false
	}
	for _, repo := range s.auth.repositories() {
		if repo.prefix != vaults[0].Prefix || repo.host == nil {
			continue
		}
		if asker, ok := repo.host.(access.PullRequestHost); ok {
			return vaults[0], asker, true
		}
	}
	return nil, nil, false
}

// capitalise starts a sentence properly. The errors are written as fragments so
// they can be joined, and a page shows one on its own.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
