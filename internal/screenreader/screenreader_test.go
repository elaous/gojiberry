//go:build !(js && wasm)

package screenreader_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/chromedp/chromedp/kb"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

const page = `<!doctype html><html lang="en"><head><title>Test page</title></head><body>
<header><h1>Gojiberry editor</h1><nav aria-label="Main"><a href="/a">Home</a> <a href="/b">Docs</a></nav></header>
<main>
<h2>Second thoughts</h2>
<p>Some <strong>bold</strong> text.</p>
<ul><li>one</li><li>two</li><li>three</li></ul>
<button aria-pressed="true">Bold</button>
<button aria-pressed="false">Italic</button>
<button></button>
<label>Name <input type="text" value="Ana" required></label>
<label><input type="checkbox" checked> Subscribe</label>
<img src="x.png" alt="A red berry"><img src="y.png">
<table><caption>Scores</caption><tr><th>Name</th><th>Points</th></tr><tr><td>Ana</td><td>3</td></tr></table>
<h4>Skipped level</h4>
<div role="status" id="live"></div>
<div role="alert" id="alarm"></div>
<button id="go" onclick="document.getElementById('live').textContent='Saved.'; document.getElementById('alarm').textContent='Disk almost full'">Save</button>
</main><footer>Footer text</footer></body></html>`

const widgets = `<!doctype html><html lang="en"><head><title>Widgets</title></head><body><main>
<h1>Widgets</h1>
<div role="tablist" aria-label="Views">
  <button role="tab" aria-selected="true" id="t1">Edit</button>
  <button role="tab" aria-selected="false" id="t2">Review</button>
  <button role="tab" aria-selected="false" id="t3">Spelling</button>
</div>
<button aria-haspopup="menu" aria-expanded="false" id="m">Format</button>
<div role="dialog" aria-modal="true" aria-label="Find and replace"><label>Find <input id="f"></label> <button>Close</button></div>
<input type="range" min="0" max="10" value="4" aria-label="Zoom">
<div role="group" aria-label="Alignment"><button>Left</button></div>
</main></body></html>`

func open(t *testing.T, html string) (*e2etest.Tab, *screenreader.Reader) {
	t.Helper()
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1") // a screen reader test that skips proves nothing
	site := e2etest.NewPageSite(t, map[string]string{"/p.html": html})
	tab := site.OpenURL(t, "/p.html")
	return tab, screenreader.New(t, tab.Context())
}

func TestReadsThePageInOrderAsAScreenReaderWould(t *testing.T) {
	_, r := open(t, page)
	want := []string{
		"banner landmark",
		"Gojiberry editor, heading, level 1",
		"Main, navigation landmark",
		"Home, link",
		"Docs, link",
		"main landmark",
		"Second thoughts, heading, level 2",
		"Some bold text.",
		"list, 3 items",
		"one", "two", "three",
		"Bold, button, pressed",
		"Italic, button, not pressed",
		"unlabelled button",
		"Name, edit text, Ana, required",
		"Subscribe, checkbox, checked",
		"A red berry, graphic",
		"unlabelled graphic",
		"Scores, table, 2 rows",
		"Name, column header", "Points, column header", "Ana, cell", "3, cell",
		"Skipped level, heading, level 4",
		"Save, button",
		"content information landmark",
		"Footer text",
	}
	if got := r.ReadAll(); !slices.Equal(got, want) {
		t.Fatalf("reading order differs:\n got %q\nwant %q", got, want)
	}
	if spoken := r.Spoken(); len(spoken) != len(want) {
		t.Fatalf("what was read is logged: %d phrases", len(spoken))
	}
}

func TestMovesByKindLikeBrowseMode(t *testing.T) {
	_, r := open(t, page)
	say := func(f func() (string, bool)) string {
		t.Helper()
		s, ok := f()
		if !ok {
			t.Fatal("nothing found")
		}
		return s
	}
	if got := say(func() (string, bool) { return r.NextHeading() }); got != "Gojiberry editor, heading, level 1" {
		t.Fatalf("%q", got)
	}
	if got := say(func() (string, bool) { return r.NextHeading(4) }); got != "Skipped level, heading, level 4" {
		t.Fatalf("a heading of one level: %q", got)
	}
	r.Reset()
	for _, want := range []string{"banner landmark", "Main, navigation landmark", "main landmark", "content information landmark"} {
		if got := say(r.NextLandmark); got != want {
			t.Fatalf("landmark %q, want %q", got, want)
		}
	}
	r.Reset()
	if got := say(r.NextLink); got != "Home, link" {
		t.Fatalf("%q", got)
	}
	if got := say(r.NextFormField); got != "Name, edit text, Ana, required" {
		t.Fatalf("%q", got)
	}
	if got := say(r.NextFormField); got != "Subscribe, checkbox, checked" {
		t.Fatalf("%q", got)
	}
	if got := say(r.NextTable); got != "Scores, table, 2 rows" {
		t.Fatalf("%q", got)
	}
	if got := say(r.Previous); got != "unlabelled graphic" {
		t.Fatalf("back: %q", got)
	}
	if _, ok := r.NextWhere(func(it screenreader.Item) bool { return it.Role == "slider" }); ok {
		t.Fatal("there is no slider")
	}
	if !r.MoveTo("button", "Italic") {
		t.Fatal("MoveTo")
	}
	if it, _ := r.Current(); it.Phrase != "Italic, button, not pressed" {
		t.Fatalf("%q", it.Phrase)
	}
	if got := say(r.Next); got != "unlabelled button" {
		t.Fatalf("%q", got)
	}
	if r.MoveTo("button", "No such") {
		t.Fatal("MoveTo found a button that is not there")
	}
	// at the ends nothing is said
	r.Reset()
	if _, ok := r.Previous(); !ok {
		t.Fatal("Previous from the top starts at the end")
	}
}

func TestFindsWhatAPersonCouldNotUse(t *testing.T) {
	_, r := open(t, page)
	if got := r.Unlabelled(); !slices.Equal(got, []string{"unlabelled button", "unlabelled graphic"}) {
		t.Fatalf("%q", got)
	}
	if got := r.HeadingProblems(); len(got) != 1 || !strings.Contains(got[0], `"Skipped level" is level 4 after level 2`) {
		t.Fatalf("%q", got)
	}
	if got := r.Headings(); !slices.Equal(got, []string{"Gojiberry editor, heading, level 1", "Second thoughts, heading, level 2", "Skipped level, heading, level 4"}) {
		t.Fatalf("%q", got)
	}
	_, clean := open(t, widgets)
	if got := clean.HeadingProblems(); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

func TestKeyboardFocusIsAnnounced(t *testing.T) {
	tab, r := open(t, page)
	tab.Focus("body")
	var said []string
	for range 5 {
		said = append(said, r.Tab())
	}
	want := []string{"Home, link", "Docs, link", "Bold, button, pressed", "Italic, button, not pressed", "unlabelled button"}
	if !slices.Equal(said, want) {
		t.Fatalf("Tab said %q, want %q", said, want)
	}
	if got := r.ShiftTab(); got != "Italic, button, not pressed" {
		t.Fatalf("Shift+Tab: %q", got)
	}
	if got := r.Focused(); got != "Italic, button, not pressed" {
		t.Fatalf("Focused: %q", got)
	}
}

func TestLiveRegionsAnnounce(t *testing.T) {
	tab, r := open(t, page)
	r.WatchLive()
	if got := r.Announcements(); len(got) != 0 {
		t.Fatalf("nothing yet: %v", got)
	}
	tab.Focus("#go")
	if got := r.Focused(); got != "Save, button" {
		t.Fatalf("%q", got)
	}
	r.Press(kb.Enter)
	got := r.Announcements()
	want := []screenreader.Announcement{{Text: "Saved."}, {Text: "Disk almost full", Assertive: true}}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %+v in %+v", w, got)
		}
	}
	if again := r.Announcements(); len(again) != 0 {
		t.Fatalf("announcements are taken once: %v", again)
	}
	// the live regions are items too, with their text
	if !r.MoveTo("status", "") {
		t.Fatal("the status region is a stop once it has text")
	}
}

func TestWidgetsAreDescribedWithTheirState(t *testing.T) {
	_, r := open(t, widgets)
	got := r.ReadAll()
	for _, want := range []string{
		"Widgets, heading, level 1",
		"Views, tab list",
		"Edit, tab, 1 of 3, selected",
		"Review, tab, 2 of 3, not selected",
		"Format, button, collapsed, has popup menu",
		"Find and replace, dialog, modal",
		"Find, edit text, blank",
		"Close, button",
		"Zoom, slider, 4",
		"Alignment, group",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("not read: %q\nread: %q", want, got)
		}
	}
}

func TestPressWithNothingFocusedSaysNothing(t *testing.T) {
	tab, r := open(t, "<!doctype html><title>x</title><p>No controls here.</p>")
	_ = tab
	if got := r.Tab(); got != "" {
		t.Fatalf("%q", got)
	}
	if got := r.ShiftTab(); got != "" {
		t.Fatalf("%q", got)
	}
	if got := r.ReadAll(); !slices.Equal(got, []string{"No controls here."}) {
		t.Fatalf("%q", got)
	}
}
