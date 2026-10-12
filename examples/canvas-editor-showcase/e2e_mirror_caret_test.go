//go:build !(js && wasm)

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

// Comments and suggested changes are in the mirror as text a reader says, and the comment's own entry is
// a region the commented text points to.
func TestMirrorSaysCommentsAndSuggestedChanges(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("End")
	for range 7 {
		d.Press("Shift+ArrowLeft")
	}
	d.AnswerDialog("Too vague, @bob")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	d.WaitSelector("#a11y-text mark")
	d.OK(d.Attr("#a11y-text mark", "aria-details") == d.Attr("#a11y-text-comments article", "id"), "the commented text points to its comment")

	sr := screenreader.New(t, d.Context())
	ph := phrases(sr)
	for _, want := range []string{"1 comment, complementary landmark", "Comment by you@example.com: Too vague, @bob"} {
		if !hasPhrase(ph, want) {
			t.Errorf("a screen reader should say %q; it reads:\n%s", want, strings.Join(ph, "\n"))
		}
	}
	if !hasPhrase(ph, "Commented text:") || !hasPhrase(ph, "On the text: flow, a") {
		t.Errorf("the commented text and its excerpt are said:\n%s", strings.Join(ph, "\n"))
	}

	// A reply and resolving show in the comment's entry.
	d.Click(`button[data-tab="comments"]`)
	d.Fill(".gj-comment-input", "Agreed")
	d.Click(".gj-comment-send")
	d.WaitFor("the reply in the mirror", `document.getElementById('a11y-text-comments').textContent.includes('Reply by you@example.com: Agreed')`)
	d.Click(".gj-comment-resolve")
	d.WaitFor("resolved in the mirror", `document.getElementById('a11y-text-comments').textContent.includes('(resolved)')`)

	// A suggested change is said with its author.
	d.Focus("#input")
	d.Press("Home")
	d.Press("Control+Alt+s")
	d.Type("ZED ")
	d.WaitSelector("#a11y-text ins")
	ph = phrases(sr)
	if !hasPhraseWith(ph, "Suggested addition by you@example.com") {
		t.Errorf("a suggested addition is said with its author:\n%s", strings.Join(ph, "\n"))
	}
	d.NoErrors()
}

// The mirror and the canvas share one caret: where the caret is on the canvas, the mirror marks the block
// and puts its own selection; a selection made in the mirror (a screen reader's cursor) moves the canvas
// caret, and a selection range becomes the canvas selection.
func TestMirrorCaretFollowsBothWays(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.CanvasClick(240, 164)
	d.WaitSelector(`#a11y-text [aria-current="location"]`)
	text := []rune(d.State().Str("text"))
	caret := d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`)
	d.OK(strings.HasPrefix(d.Text(`#a11y-text [aria-current="location"]`), "Everything on this canvas"), "the block with the caret is marked: "+d.Text(`#a11y-text [aria-current="location"]`))
	d.WaitFor("the mirror's own selection", `window.getSelection().anchorNode && document.getElementById('a11y-text').contains(window.getSelection().anchorNode)`)
	before := d.Str(`(s => { const r = document.createRange(); const n = s.anchorNode; const b = (n.nodeType === 1 ? n : n.parentElement).closest('[data-gj-start]'); r.setStart(b, 0); r.setEnd(s.anchorNode, s.anchorOffset); return r.toString(); })(window.getSelection())`)
	start := d.Int(`+document.querySelector('#a11y-text [aria-current="location"]').dataset.gjStart`)
	d.OK(start+len([]rune(before)) == caret, fmt.Sprint("the selection in the mirror is at the canvas caret: ", start, " ", len([]rune(before)), " ", caret))

	// A screen reader moves its cursor in the mirror: the canvas caret follows.
	const flows = `[...document.querySelectorAll('#a11y-text p')].find(p => p.textContent.startsWith('The text flows'))`
	d.Eval(`(() => { const p = `+flows+`; const r = document.createRange(); r.setStart(p.firstChild, 9); r.collapse(true); const s = getSelection(); s.removeAllRanges(); s.addRange(r); })()`, nil)
	at := len([]rune(string(text)[:strings.Index(string(text), "The text flows")])) + 9
	d.Wait("s.cursors[0].Caret === " + e2etest.J(at) + " && s.cursors[0].Anchor === s.cursors[0].Caret")

	// A range selected in the mirror is the canvas selection.
	d.Eval(`(() => { const p = `+flows+`; const r = document.createRange(); r.setStart(p.firstChild, 4); r.setEnd(p.firstChild, 9); const s = getSelection(); s.removeAllRanges(); s.addRange(r); })()`, nil)
	d.Wait("s.cursors[0].Anchor === " + e2etest.J(at-5) + " && s.cursors[0].Caret === " + e2etest.J(at))
	d.WaitFor("the selection said", `!!document.querySelector('.gj-a11y-selection')`)
	d.OK(d.Text(".gj-a11y-selection") == "Selected: text ", "the selection is said: "+d.Text(".gj-a11y-selection"))
	// ...and it stays: the mirror putting its own selection back must not collapse it.
	d.Sleep(400)
	d.OK(d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Anchor`) == at-5 && d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`) == at, "the canvas selection stays")
	d.NoErrors()
}
