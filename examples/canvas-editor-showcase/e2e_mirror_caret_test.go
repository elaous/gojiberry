//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/internal/screenreader"
)

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

	d.Click(`button[data-tab="comments"]`)
	d.Fill(".gj-comment-input", "Agreed")
	d.Click(".gj-comment-send")
	d.WaitFor("the reply in the mirror", `document.getElementById('a11y-text-comments').textContent.includes('Reply by you@example.com: Agreed')`)
	d.Click(".gj-comment-resolve")
	d.WaitFor("resolved in the mirror", `document.getElementById('a11y-text-comments').textContent.includes('(resolved)')`)

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

func TestMirrorCaretFollowsBothWays(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.CanvasClick(240, 164)
	d.WaitSelector(`#a11y-text [aria-current="location"]`)
	text := []rune(d.State().Str("text"))
	caret := d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`)
	d.OK(strings.HasPrefix(d.Text(`#a11y-text [aria-current="location"]`), "Everything on this canvas"), "the block with the caret is marked: "+d.Text(`#a11y-text [aria-current="location"]`))
	d.Sleep(400)
	d.OK(d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`) == caret, "the canvas caret stays where the click put it")

	const flows = `[...document.querySelectorAll('#a11y-text p')].find(p => p.textContent.startsWith('The text flows'))`
	d.Eval(`(() => { const p = `+flows+`; const r = document.createRange(); r.setStart(p.firstChild, 9); r.collapse(true); const s = getSelection(); s.removeAllRanges(); s.addRange(r); })()`, nil)
	at := len([]rune(string(text)[:strings.Index(string(text), "The text flows")])) + 9
	d.Wait("s.cursors[0].Caret === " + e2etest.J(at) + " && s.cursors[0].Anchor === s.cursors[0].Caret")

	d.Eval(`(() => { const p = `+flows+`; const r = document.createRange(); r.setStart(p.firstChild, 4); r.setEnd(p.firstChild, 9); const s = getSelection(); s.removeAllRanges(); s.addRange(r); })()`, nil)
	d.Wait("s.cursors[0].Anchor === " + e2etest.J(at-5) + " && s.cursors[0].Caret === " + e2etest.J(at))
	d.WaitFor("the selection said", `!!document.querySelector('.gj-a11y-selection')`)
	d.OK(d.Text(".gj-a11y-selection") == "Selected: text ", "the selection is said: "+d.Text(".gj-a11y-selection"))
	d.Sleep(400)
	d.OK(d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Anchor`) == at-5 && d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`) == at, "the canvas selection stays")

	d.NoErrors()
}

func TestMirrorSelectionFollowsCaretOutsideFields(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`(() => { const i = document.getElementById('input'); i.blur(); i.focus = () => {}; })()`, nil)
	d.CanvasClick(240, 164)
	d.WaitFor("the mirror selection at the canvas caret", `(() => { const s = getSelection(); const m = document.getElementById('a11y-text'); return s.rangeCount > 0 && s.isCollapsed && m.contains(s.anchorNode); })()`)
	first := d.Str(`(() => { let n = getSelection().anchorNode; n = n.nodeType === 1 ? n : n.parentElement; const b = n.closest('[data-gj-start]'); return b ? b.dataset.gjStart : ''; })()`)
	d.CanvasClick(240, 420)
	d.WaitFor("the mirror selection moved with the caret", `(() => { let n = getSelection().anchorNode; n = n.nodeType === 1 ? n : n.parentElement; const b = n.closest('[data-gj-start]'); return b ? b.dataset.gjStart : ''; })() !== `+e2etest.J(first))
	d.NoErrors()
}

func TestMirrorMapsEmojiAndFootnoteMarks(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.Type("😀é")
	for range 10 {
		addFootnote(d, "note")
	}
	d.Wait("s.footnotes.length === 10")
	d.Type("zz")
	const block = `document.querySelector('#a11y-text [data-gj-start="17"]')`
	d.WaitFor("the marks in the copy", block+`.querySelectorAll('sup[data-gj-stream="1"]').length === 10`)
	d.OK(strings.HasPrefix(d.Str(block+".textContent"), "😀é12345678910zz"), "the copy has the marks numbered: "+d.Str(block+".textContent"))

	d.Eval(`(() => { const i = document.getElementById('input'); i.blur(); i.focus = () => {}; })()`, nil)
	caret := func() int {
		return d.Int(`JSON.parse(document.getElementById('state').textContent).cursors[0].Caret`)
	}
	place := func(js string) {
		d.Eval(`(() => { const b = `+block+`; const w = document.createTreeWalker(b, 4); const t = []; for (let n; (n = w.nextNode());) t.push(n); `+js+` })()`, nil)
	}
	place(`const r = document.createRange(); r.setStart(t[0], 2); r.collapse(true); const s = getSelection(); s.removeAllRanges(); s.addRange(r);`)
	d.Wait("s.cursors[0].Caret === 18")
	place(`const r = document.createRange(); r.setStart(t[t.length - 2], 1); r.collapse(true); const s = getSelection(); s.removeAllRanges(); s.addRange(r);`)
	d.Wait("s.cursors[0].Caret === 17 + 2 + 10")
	place(`const r = document.createRange(); r.setStart(t[t.length - 1], 2); r.collapse(true); const s = getSelection(); s.removeAllRanges(); s.addRange(r);`)
	d.Wait("s.cursors[0].Caret === 17 + 2 + 10 + 2")
	d.OK(caret() == 31, "the canvas caret is after zz")
	d.NoErrors()
}

func TestMirrorUpdatesOnlyTheBlocksThatChanged(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Home")
	d.WaitSelector(`#a11y-text [data-gj-start="17"]`)
	d.Eval(`(() => { document.querySelectorAll('#a11y-text > *, #a11y-text [data-gj-start]').forEach((e, i) => { e.__kept = i + 1; }); })()`, nil)
	heading := `document.querySelector('#a11y-text h1[data-gj-start="0"]')`
	body := `document.querySelector('#a11y-text p[data-gj-start="17"]')`
	d.Type("new ")
	d.WaitFor("the edit in the copy", body+`.textContent.startsWith('new Everything')`)
	d.OK(d.Bool(heading+`.__kept > 0`), "the heading, which was not edited, is the same element")
	d.OK(d.Bool(body+`.__kept > 0`), "the paragraph that was edited is updated in place")
	d.NoErrors()
}

func TestFloatingNoteIsReadWhereItIsOnTheSheet(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.WaitSelector("#a11y-text")
	order := d.Str(`[...document.querySelectorAll('#a11y-text > *:not(nav):not(aside)')].map(e => e.textContent.slice(0, 20)).join('|')`)
	note := strings.Index(order, "Note: Floating note")
	above := strings.Index(order, "Everything on this")
	below := strings.Index(order, "Second thoughts")
	if note < 0 || above < 0 || below < 0 || above >= note || note >= below {
		t.Errorf("the note is in the wrong place: %s", order)
	}
	d.NoErrors()
}
