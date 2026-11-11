//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

func openTyping(t *testing.T, prefs string) (*e2etest.Tab, *screenreader.Reader) {
	t.Helper()
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	if prefs != "" {
		d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify(`+prefs+`))`, nil)
		d.Reload()
		d.WaitSelector("canvas")
	}
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	sr := screenreader.New(t, d.Context())
	sr.WatchLive()
	sr.Announcements()
	return d, sr
}

func said(t *testing.T, d *e2etest.Tab, sr *screenreader.Reader, want ...string) string {
	t.Helper()
	var out []string
	for range 80 {
		for _, a := range sr.Announcements() {
			out = append(out, a.Text)
		}
		all := strings.Join(out, " | ")
		done := len(out) > 0
		for _, w := range want {
			done = done && strings.Contains(all, w)
		}
		if done {
			return all
		}
		d.Sleep(100)
	}
	return strings.Join(out, " | ")
}

func TestTypingSpeaksFinishedWords(t *testing.T) {
	d, sr := openTyping(t, `{typing:'words'}`)
	d.OK(d.Attr("#typing-live", "aria-live") == "polite" && d.Attr("#typing-live", "role") == "status", "typed words go to a polite status region")
	d.Type("alpha beta gamma ")
	got := said(t, d, sr, "alpha", "beta", "gamma")
	for _, w := range []string{"alpha", "beta", "gamma"} {
		d.OK(strings.Contains(got, w), "the word "+w+" is said: "+got)
	}
	d.OK(!strings.Contains(got, "a | l"), "and not letter by letter: "+got)
	d.NoErrors()
}

func TestTypingSpeaksCharactersWhenAsked(t *testing.T) {
	d, sr := openTyping(t, `{typing:'characters'}`)
	d.Type("q ")
	got := said(t, d, sr, "q", "space")
	d.OK(strings.Contains(got, "q") && strings.Contains(got, "space"), "a letter and a space are said: "+got)
	d.NoErrors()
}

func TestTypingSaysNothingUnlessAsked(t *testing.T) {
	d, sr := openTyping(t, "")
	d.Type("alpha beta ")
	d.Press("ArrowDown")
	d.Press("Control+b")
	d.Sleep(1500)
	d.OK(strings.TrimSpace(d.Text("#typing-live")) == "", "the typing region is empty: "+d.Text("#typing-live"))
	for _, a := range sr.Announcements() {
		d.OK(!strings.Contains(a.Text, "alpha") && !strings.Contains(a.Text, "Bold"), "no live region says what was typed: "+a.Text)
	}
	d.NoErrors()
}

func TestTypingReadsTheLineAndFormattingChanges(t *testing.T) {
	d, sr := openTyping(t, `{typeread:'on', typefmt:'on'}`)
	text := d.State().Str("text")
	d.Press("ArrowDown")
	got := said(t, d, sr, "")
	line := d.Str(`document.getElementById('typing-live').textContent`)
	d.OK(strings.TrimSpace(line) != "" && strings.Contains(text, strings.TrimSpace(line)), "the next line is read: "+got)
	d.OK(!strings.HasPrefix(strings.TrimSpace(text), strings.TrimSpace(line)), "and it is not the first line")

	d.Press("Control+b")
	got = said(t, d, sr, "Bold on")
	d.OK(strings.Contains(got, "Bold on"), "bold is said: "+got)
	d.Press("Control+b")
	got = said(t, d, sr, "Bold off")
	d.OK(strings.Contains(got, "Bold off"), "and its end: "+got)
	d.NoErrors()
}

func TestTypingPreferencesAreInTheMenu(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.Click(`.gj-menubar-panel .gj-menu-label:text-is("Typing: say each word")`)
	d.WaitFor("the choice kept", `JSON.parse(localStorage.getItem('showcase:a11y')).typing === 'words'`)
	d.OK(strings.Contains(d.Text(".gj-announcer"), "Typing: say each word"), "the choice is announced: "+d.Text(".gj-announcer"))
	d.NoErrors()
}
