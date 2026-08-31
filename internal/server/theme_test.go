package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The choice has to be on the html element in the response itself. A script
// that swapped the colours after the page painted would flash the wrong theme
// on every load, which is the one thing a theme switch must not do.
func TestTheThemeArrivesWithThePage(t *testing.T) {
	_, h, _ := newServer(t)

	// Nothing chosen: no attribute, so the media query decides.
	body := as(t, h, nil, "GET", "/", nil).Body.String()
	if strings.Contains(body, "data-theme") {
		t.Error("with nothing chosen the page should follow the system")
	}

	w := as(t, h, nil, "POST", "/theme", url.Values{"theme": {"dark"}, "next": {"/"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("choosing dark: code = %d", w.Code)
	}
	var chosen *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == themeCookie {
			chosen = c
		}
	}
	if chosen == nil || chosen.Value != "dark" {
		t.Fatalf("the choice was not remembered: %+v", chosen)
	}

	body = as(t, h, chosen, "GET", "/", nil).Body.String()
	if !strings.Contains(body, `data-theme="dark"`) {
		t.Error("the page does not arrive in the chosen theme")
	}
	if strings.Contains(body, "<script") && !strings.Contains(body, "board.js") {
		t.Error("the theme should need no script")
	}
}

// Following the system has to stay reachable: somebody who tried dark should be
// able to get back to whatever the laptop is doing.
func TestGoingBackToTheSystemForgetsTheChoice(t *testing.T) {
	_, h, _ := newServer(t)

	dark := as(t, h, nil, "POST", "/theme", url.Values{"theme": {"dark"}}).Result().Cookies()[0]
	w := as(t, h, dark, "POST", "/theme", url.Values{"theme": {"system"}})

	for _, c := range w.Result().Cookies() {
		if c.Name == themeCookie && c.MaxAge >= 0 {
			t.Errorf("the choice was replaced rather than forgotten: %+v", c)
		}
	}
	if body := as(t, h, nil, "GET", "/", nil).Body.String(); strings.Contains(body, "data-theme") {
		t.Error("the page still names a theme")
	}
}

// A value nobody offered is not remembered.
func TestAThemeNobodyOfferedIsIgnored(t *testing.T) {
	_, h, _ := newServer(t)

	w := as(t, h, nil, "POST", "/theme", url.Values{"theme": {"neon"}})
	for _, c := range w.Result().Cookies() {
		if c.Name == themeCookie && c.Value == "neon" {
			t.Error("an unknown theme was remembered")
		}
	}
}

// The switch says which one is in force, so it reads as a state rather than as
// three things to try.
func TestTheSwitchSaysWhichThemeIsOn(t *testing.T) {
	_, h, _ := newServer(t)

	body := as(t, h, nil, "GET", "/", nil).Body.String()
	if strings.Count(body, `class="theme-pick`) != 3 {
		t.Errorf("the switch does not offer three choices:\n%s", body)
	}
	if !strings.Contains(body, `class="theme-pick on"`) {
		t.Error("none of the choices is marked as the one in force")
	}
}
