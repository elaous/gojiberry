package components

import (
	"sync"

	"github.com/elaous/gojiberry/xango"
)

type Asker struct {
	mu                               sync.Mutex
	message                          string
	label                            string // the confirm button's words for this question; "" is ConfirmLabel
	do                               func()
	onChange                         func()
	Title, ConfirmLabel, CancelLabel string
}

func NewAsker(onChange func()) *Asker { return &Asker{onChange: onChange} }

func (a *Asker) Ask(message string, do func()) { a.AskWith("", message, do) }

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

func confirmedAs(askAs func(label, message string, do func()), ask func(message string, do func()), label, message string, do func()) func() {
	if askAs == nil {
		return confirmed(ask, message, do)
	}
	return func() { askAs(label, message, do) }
}
