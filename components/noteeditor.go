package components

import (
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/elaous/gojiberry/xango"
)

// NoteEditor is the dialog a footnote is typed in: a text box and, under it, a read-only counter of how much of the
// limit is used ("120 of 1000 characters"). The counter exists only while the dialog is open, that is only while a
// note is being typed. The host puts Dialog in its tree once and gives the canvas Stage.EditNote = ed.Edit.
//
//	ed := components.NewNoteEditor(rerender)
//	stage.EditNote = ed.Edit
//	// and somewhere in the page: ed.Dialog()
type NoteEditor struct {
	mu       sync.Mutex
	open     bool
	title    string
	draft    string
	max      int
	done     func(text string, ok bool)
	onChange func()
	// SaveLabel and CancelLabel are the buttons' words (translated defaults "Save" and "Cancel").
	SaveLabel, CancelLabel string
}

// NewNoteEditor makes a NoteEditor that calls onChange whenever it opens, closes or its text changes.
func NewNoteEditor(onChange func()) *NoteEditor { return &NoteEditor{onChange: onChange} }

// Edit opens the dialog on initial. done is called once, with the text and true when it is saved, or false when the
// person cancels. A dialog already open is cancelled first. max is the limit of characters (0: none shown).
func (n *NoteEditor) Edit(title, initial string, max int, done func(text string, ok bool)) {
	n.mu.Lock()
	prev := n.done
	n.open, n.title, n.draft, n.max, n.done = true, title, initial, max, done
	n.mu.Unlock()
	if prev != nil {
		prev("", false)
	}
	n.notify()
}

// Open reports whether the dialog is open.
func (n *NoteEditor) Open() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.open
}

// Draft is the text typed so far.
func (n *NoteEditor) Draft() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.draft
}

func (n *NoteEditor) notify() {
	if n.onChange != nil {
		n.onChange()
	}
}

func (n *NoteEditor) typed(text string) {
	n.mu.Lock()
	if n.max > 0 {
		if rs := []rune(text); len(rs) > n.max {
			text = string(rs[:n.max])
		}
	}
	n.draft = text
	n.mu.Unlock()
	n.notify()
}

func (n *NoteEditor) finish(ok bool) {
	n.mu.Lock()
	done, text := n.done, n.draft
	n.open, n.done, n.draft = false, nil, ""
	n.mu.Unlock()
	if done != nil {
		done(text, ok)
	}
	n.notify()
}

// NoteCounter is the words of the counter: how many characters are used of the limit.
func NoteCounter(used, max int) string {
	return trf("note.counter", "%d of %d characters", used, max)
}

// Dialog is the editor for the open note, closed when there is none.
func (n *NoteEditor) Dialog() xango.Node {
	n.mu.Lock()
	open, title, draft, max := n.open, n.title, n.draft, n.max
	n.mu.Unlock()
	save, cancel := n.SaveLabel, n.CancelLabel
	if save == "" {
		save = tr("note.save", "Save")
	}
	if cancel == "" {
		cancel = tr("confirm.no", "Cancel")
	}
	if title == "" {
		title = tr("note.title", "Footnote")
	}
	area := []xango.Attr{xango.A("class", "gj-textarea gj-note-text"), xango.A("rows", "6"), xango.A("value", draft),
		xango.A("aria-label", title), xango.A("aria-describedby", "gj-note-counter"),
		xango.On("input", func(e xango.Event) { n.typed(xango.EventValue[string](e)) })}
	if max > 0 {
		area = append(area, xango.A("maxlength", strconv.Itoa(max)))
	}
	body := []xango.Node{xango.El("textarea", area)}
	if max > 0 {
		used := utf8.RuneCountInString(draft)
		class := "gj-note-counter"
		if used >= max {
			class += " gj-note-counter-full"
		}
		// Read-only text: a status the screen reader announces politely as the count changes.
		body = append(body, xango.El("output", []xango.Attr{xango.A("id", "gj-note-counter"), xango.A("class", class), xango.A("role", "status"), xango.A("aria-live", "polite")},
			xango.Text(NoteCounter(used, max))))
	}
	return Dialog(DialogProps{Open: open, Title: title, Body: body, Actions: []xango.Node{
		Button(ButtonProps{Label: cancel, Variant: ButtonSecondary, OnClick: func(xango.Event) { n.finish(false) }}),
		Button(ButtonProps{Label: save, Variant: ButtonPrimary, OnClick: func(xango.Event) { n.finish(true) }}),
	}, OnClose: func() { n.finish(false) }})
}
