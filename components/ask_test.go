package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/user"
	"github.com/elaous/gojiberry/xango"
)

func TestAskerConfirmsBeforeADestructiveAction(t *testing.T) {
	changes := 0
	ask := NewAsker(func() { changes++ })
	mustNotContain(t, render(t, ask.Dialog()), "Delete forever")
	done := 0
	ask.Ask("Delete a forever?", func() { done++ })
	if !ask.Asking() || done != 0 {
		t.Fatal("the action must wait for the answer")
	}
	html := render(t, ask.Dialog())
	mustContain(t, html, "Delete a forever?", `role="alertdialog"`, "Are you sure?")
	n := ask.Dialog()
	clicks := findHandlers(n, "click")
	clicks[0](xango.Event{})
	if done != 0 || ask.Asking() {
		t.Fatalf("cancel: done=%d asking=%v", done, ask.Asking())
	}
	ask.Ask("again?", func() { done++ })
	confirm := findHandlers(ask.Dialog(), "click")
	confirm[len(confirm)-1](xango.Event{}) // the Delete button is the last
	if done != 1 || ask.Asking() || changes < 4 {
		t.Fatalf("confirm: done=%d asking=%v changes=%d", done, ask.Asking(), changes)
	}
}

func TestDeletesAskFirstWhenThereIsAnAsker(t *testing.T) {
	var asked []string
	var pending []func()
	ask := func(msg string, do func()) { asked = append(asked, msg); pending = append(pending, do) }
	var deleted [][]string
	del := func(_ context.Context, ids []string) error { deleted = append(deleted, ids); return nil }
	p := &Pending{Inline: true}
	trash := Trash(TrashProps{Docs: []Doc{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, Delete: del, Pending: p, Confirm: ask})
	for _, h := range findHandlers(trash, "click") {
		h(xango.Event{})
	}
	if len(deleted) != 0 || len(asked) != 3 { // Empty trash, then one per row
		t.Fatalf("asked %q, deleted %v", asked, deleted)
	}
	if !strings.Contains(asked[0], "all 2 documents") || !strings.Contains(asked[1], "Alpha") {
		t.Errorf("the questions: %q", asked)
	}
	for _, do := range pending {
		do()
	}
	if len(deleted) != 3 {
		t.Errorf("deleted %v after confirming", deleted)
	}
	deleted = nil
	for _, h := range findHandlers(Trash(TrashProps{Docs: []Doc{{ID: "a", Name: "A"}}, Delete: del, Pending: p}), "click") {
		h(xango.Event{})
	}
	if len(deleted) != 2 {
		t.Errorf("no asker: %v", deleted)
	}
	asked = nil
	docs := DocList(DocListProps{Docs: []Doc{{ID: "a", Name: "Alpha"}}, Delete: func(context.Context, string) error { return nil }, Pending: p, Confirm: ask})
	findHandlers(docs, "click")[0](xango.Event{})
	people := People(PeopleProps{People: []user.Person{{ID: "me", Role: "owner"}, {ID: "ana", Role: "editor"}}, Remove: func(context.Context, string) error { return nil }, Pending: p, Confirm: ask})
	for _, h := range findHandlers(people, "click") {
		h(xango.Event{})
	}
	if len(asked) != 2 || !strings.Contains(asked[1], "ana") {
		t.Errorf("asked %q", asked)
	}
}
