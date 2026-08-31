package server

import (
	"net/http"
	"strings"
)

// Light or dark, or whatever the system says.
//
// The colours followed prefers-color-scheme and nothing else, which is right
// as a default and wrong as the only option: somebody reads a board all day in
// a room the operating system knows nothing about.
//
// The choice is a cookie the server reads, and it stamps data-theme on the html
// element, so the page arrives already in the right colours. The alternative —
// a script that swaps them after the page paints — shows a flash of the wrong
// theme on every single load, which is the one thing a theme switch must not do.
// It also means this works with scripting off, like everything else here.
//
// Three states, not two. "Follow the system" has to stay reachable: a laptop
// that goes dark in the evening should take the board with it, and somebody who
// tried dark once should be able to get that back rather than being stuck with
// whichever they last clicked.

// themeCookie remembers the choice. It holds a word, not a secret, and lasts a
// year because a preference is not a session.
const themeCookie = "docket_theme"

// Themes, in the order the switch offers them.
const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

// themeOf is the choice this request carries, or system when there is none.
func themeOf(r *http.Request) string {
	cookie, err := r.Cookie(themeCookie)
	if err != nil {
		return themeSystem
	}
	switch strings.TrimSpace(cookie.Value) {
	case themeLight:
		return themeLight
	case themeDark:
		return themeDark
	}
	return themeSystem
}

// themeAttribute is what goes on the html element: nothing for system, so the
// media query decides.
func themeAttribute(theme string) string {
	if theme == themeLight || theme == themeDark {
		return theme
	}
	return ""
}

// themeChoice is one option on the switch.
type themeChoice struct {
	Value string
	Label string
	On    bool
}

func themeChoices(current string) []themeChoice {
	return []themeChoice{
		{themeSystem, "System", current == themeSystem},
		{themeLight, "Light", current == themeLight},
		{themeDark, "Dark", current == themeDark},
	}
}

// handleTheme records the choice and returns to the page it was made on.
func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	chosen := strings.TrimSpace(r.FormValue("theme"))
	switch chosen {
	case themeLight, themeDark:
		http.SetCookie(w, &http.Cookie{
			Name:     themeCookie,
			Value:    chosen,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil,
			MaxAge:   365 * 24 * 60 * 60,
		})
	default:
		// Back to following the system, which is forgetting rather than
		// remembering something else.
		clearCookie(w, themeCookie)
	}
	http.Redirect(w, r, backTo(r.FormValue("next")), http.StatusSeeOther)
}
