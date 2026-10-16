package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestNoteEditorShowsTheCounterOnlyWhileOpenAndKeepsToTheLimit(t *testing.T) {
	changes := 0
	ed := NewNoteEditor(func() { changes++ })
	closed := render(t, ed.Dialog())
	mustNotContain(t, closed, "gj-note-counter")
	mustNotContain(t, closed, "characters")

	var got []string
	ed.Edit("Footnote", "abc", 10, func(text string, ok bool) {
		got = append(got, text+"|"+map[bool]string{true: "saved", false: "cancelled"}[ok])
	})
	html := render(t, ed.Dialog())
	mustContain(t, html, `id="gj-note-counter"`, "3 of 10 characters", `role="status"`, `maxlength="10"`, `aria-live="polite"`)
	mustNotContain(t, html, "gj-note-counter-full")

	// Typing updates the count and stops at the limit.
	n := ed.Dialog()
	for _, h := range findHandlers(n, "input") {
		h(xango.NewEvent("input", "abcdefghijKLMNOP", false, func() {}, nil))
	}
	if d := ed.Draft(); d != "abcdefghij" {
		t.Fatalf("draft %q", d)
	}
	html = render(t, ed.Dialog())
	mustContain(t, html, "10 of 10 characters", "gj-note-counter-full")

	// Save reports the text; the counter is gone with the dialog.
	ed.finish(true)
	if len(got) != 1 || got[0] != "abcdefghij|saved" || ed.Open() {
		t.Fatalf("got %v open=%v", got, ed.Open())
	}
	mustNotContain(t, render(t, ed.Dialog()), "characters")

	// Cancel, and a second Edit replacing an open one cancels the first.
	ed.Edit("Footnote", "", 5, func(text string, ok bool) {
		got = append(got, text+"|"+map[bool]string{true: "saved", false: "cancelled"}[ok])
	})
	ed.Edit("Footnote", "x", 5, func(string, bool) {})
	if !strings.HasSuffix(got[len(got)-1], "|cancelled") {
		t.Fatalf("the first dialog was cancelled: %v", got)
	}
	if changes < 4 {
		t.Fatalf("the host was told %d times", changes)
	}
}

func TestNoteCounterWords(t *testing.T) {
	if NoteCounter(12, 1000) != "12 of 1000 characters" {
		t.Fatal(NoteCounter(12, 1000))
	}
}
