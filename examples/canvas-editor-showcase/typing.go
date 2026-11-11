//go:build js && wasm

package main

import (
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
)

func (u *ui) noteTyping(flow func() *canvas.TextFlow, renderUI func()) {
	u.typing.Level = curPrefs.Typing
	u.typing.Formatting = curPrefs.TypeFmt == "on"
	u.typing.Lines = curPrefs.TypeRead == "on"
	fl := flow()
	if fl == nil || !fl.Editing() || (u.typing.Level == components.TypingOff && !u.typing.Formatting && !u.typing.Lines) {
		u.typing.Reset()
		// when editing starts again the document is only remembered, not pasted
		return
	}
	if msg := u.typing.Update(time.Now(), typingState(fl, u.typing.Formatting)); msg != "" {
		u.typingMsg = msg
		u.typingN++
		renderUI()
	}
	if u.typing.Pending() && !u.typingTimer {
		u.typingTimer = true
		time.AfterFunc(550*time.Millisecond, func() {
			u.typingTimer = false
			u.noteTyping(flow, renderUI)
		})
	}
}

func typingState(fl *canvas.TextFlow, formatting bool) components.TypingState {
	text := fl.EditText()
	caret := fl.Cursors()[0].Caret
	st := components.TypingState{Text: text, Caret: caret}
	runes := []rune(text)
	lines := fl.Layout().Lines
	for i, ln := range lines {
		if caret <= ln.End || i == len(lines)-1 {
			start, end := min(max(ln.Start, 0), len(runes)), min(max(ln.End, 0), len(runes))
			st.Line, st.LineStart = string(runes[start:max(start, end)]), start
			break
		}
	}
	if formatting {
		f := fl.CaretFormat()
		add := func(on bool, name string) {
			if on {
				st.Marks = append(st.Marks, name)
			}
		}
		add(f.FontWeight >= 600, T("typing.bold", "Bold"))
		add(f.FontStyle == "italic" || f.FontStyle == "oblique", T("typing.italic", "Italic"))
		add(f.Underline, T("typing.underline", "Underline"))
		add(f.Strike, T("typing.strike", "Strikethrough"))
		add(f.Superscript, T("typing.superscript", "Superscript"))
		add(f.Subscript, T("typing.subscript", "Subscript"))
		add(f.Link != "", T("typing.link", "Link"))
		st.Block = fl.ParagraphAt(caret).Style
	}
	return st
}
