package components

import (
	"sync"

	"github.com/elaous/gojiberry/xango"
)

// Asker puts a yes/no question to the user before something that cannot be
// undone, using Confirm. The document components take its Ask as their Confirm
// prop: with one, "Delete forever" asks first; without, it just happens. The
// host renders Dialog once, anywhere in its tree.
//
//	ask := components.NewAsker(rerender)
//	components.Trash(components.TrashProps{..., Confirm: ask.Ask})
//	// and somewhere in the page: ask.Dialog()
type Asker struct {
	mu       sync.Mutex
	message  string
	label    string // the confirm button's words for this question; "" is ConfirmLabel
	do       func()
	onChange func()
	// Title, ConfirmLabel and CancelLabel are the dialog's words; defaults are
	// "Are you sure?", "Delete" and "Cancel", translated through Translate.
	Title, ConfirmLabel, CancelLabel string
}

// NewAsker makes an Asker that calls onChange whenever a question is put or answered,
// so the host renders again.
func NewAsker(onChange func()) *Asker { return &Asker{onChange: onChange} }

// Ask shows the question; do runs if the user confirms it. A question already
// open is replaced.
func (a *Asker) Ask(message string, do func()) { a.AskWith("", message, do) }

// AskWith is Ask with the words of the confirm button for this question ("Make confidential"
// rather than the default "Delete"); "" keeps the default.
func (a *Asker) AskWith(label, message string, do func()) {
	a.mu.Lock()
	a.message, a.label, a.do = message, label, do
	a.mu.Unlock()
	a.notify()
}

// Asking reports whether a question is open.
func (a *Asker) Asking() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.do != nil
}

func (a *Asker) notify() {
	if a.onChange != nil {
		a.onChange()
	}
}

func (a *Asker) answer(yes bool) {
	a.mu.Lock()
	do := a.do
	a.message, a.label, a.do = "", "", nil
	a.mu.Unlock()
	if yes && do != nil {
		do()
	}
	a.notify()
}

// Dialog is the Confirm dialog for the open question, closed when there is none.
func (a *Asker) Dialog() xango.Node {
	a.mu.Lock()
	message, open, label := a.message, a.do != nil, a.label
	a.mu.Unlock()
	title := a.Title
	if title == "" {
		title = tr("confirm.title", "Are you sure?")
	}
	yes := a.ConfirmLabel
	if label != "" {
		yes = label
	}
	if yes == "" {
		yes = tr("confirm.yes", "Delete")
	}
	no := a.CancelLabel
	if no == "" {
		no = tr("confirm.no", "Cancel")
	}
	return Confirm(ConfirmProps{
		Open: open, Title: title, Message: message, ConfirmLabel: yes, CancelLabel: no,
		OnConfirm: func() { a.answer(true) },
		OnCancel:  func() { a.answer(false) },
	})
}

// confirmed runs do after asking, when there is an asker (a nil ask runs it at
// once).
func confirmed(ask func(message string, do func()), message string, do func()) func() {
	if do == nil {
		return nil
	}
	return func() {
		if ask == nil {
			do()
			return
		}
		ask(message, do)
	}
}

// confirmedAs is confirmed for a question whose confirm button says label: with askAs (an
// Asker's AskWith) the dialog uses that word, else the plain ask is used.
func confirmedAs(askAs func(label, message string, do func()), ask func(message string, do func()), label, message string, do func()) func() {
	if askAs == nil {
		return confirmed(ask, message, do)
	}
	return func() { askAs(label, message, do) }
}
