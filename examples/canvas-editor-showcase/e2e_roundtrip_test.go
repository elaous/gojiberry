//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestCommentsAndFootnotesSurviveSaveAndOpen(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.ToggleMode()
	body := func() { d.CanvasClick(240, 164) }
	selectWord := func() {
		body()
		d.Focus("#input")
		d.Press("End")
		for range 7 {
			d.Focus("#input")
			d.Press("Shift+ArrowLeft")
		}
		d.Focus("#input")
	}
	selectWord()
	d.AnswerDialog("Review this")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	body()
	d.Focus("#input")
	d.Press("End")
	addFootnote(d, "A note")
	d.Wait("s.footnotes.length === 1")

	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "with notes")
	d.Click(".gj-saver-save")
	d.WaitSelector(".gj-doclist-item")
	d.Click(`button[data-tab="comments"]`)
	d.Click(".gj-comment-delete")
	d.Wait("s.comments === 0")
	d.Click(`button[data-tab="outline"]`)
	d.Click(".gj-footnote-delete")
	d.Wait("s.footnotes.length === 0")
	d.Click(`button[data-tab="save"]`)
	d.Fill(".gj-saver-name", "plain")
	d.Click(".gj-saver-save-new")
	d.WaitFor("two documents", "document.querySelectorAll('.gj-doclist-item').length === 2")

	open := func(name string) {
		d.Click(`.gj-doclist-item:has(.gj-doc-name:text-is("` + name + `")) .gj-doclist-open`)
		d.WaitFor(name+" to be the open one", `(document.querySelector('.gj-doclist-current .gj-doc-name') || {textContent: ''}).textContent === '`+name+`'`)
	}
	open("with notes")
	d.Wait("s.comments === 1 && s.footnotes.length === 1")
	open("plain")
	d.Wait("s.comments === 0 && s.footnotes.length === 0")
}

func addFootnote(d *e2etest.Tab, text string) {
	d.Press("Control+Alt+f")
	d.WaitSelector(".gj-note-text")
	d.Fill(".gj-note-text", text)
	d.Click(`.gj-dialog-actions button:has-text("Save")`)
	d.Wait("!document.querySelector('.gj-note-text')")
	d.Focus("#input")
}
