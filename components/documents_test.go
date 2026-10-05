package components

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/xango"
)

type fakeTrash struct{ restored, purged [][]string }

func (f *fakeTrash) Restore(_ context.Context, ids ...string) error {
	f.restored = append(f.restored, ids)
	return nil
}
func (f *fakeTrash) Purge(_ context.Context, ids ...string) error {
	f.purged = append(f.purged, ids)
	return nil
}

func TestTrashPlugsIntoABackendAndActsOnIDs(t *testing.T) {
	gone := []Doc{{ID: "a", Name: "same name", Trashed: time.Now()}, {ID: "b", Name: "same name", Trashed: time.Now()}}
	var b fakeTrash
	p := TrashProps{Docs: gone, Pending: &Pending{Inline: true},
		Children: []xango.Node{xango.El("p", nil, xango.Text("the live list"))}}.Use(&b)
	n := Trash(p)
	html := render(t, n)
	mustContain(t, html, "the live list", `data-id="a"`, `data-id="b"`, "Empty trash", "Restore", "Delete forever")
	// Two documents with one name are two rows with their own IDs.
	if strings.Count(html, "same name") != 2 {
		t.Errorf("both documents listed: %s", html)
	}
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	// Empty trash (first in the header) acts on all of them, row buttons on their own ID.
	if len(b.restored) != 2 || b.restored[0][0] != "a" || b.restored[1][0] != "b" {
		t.Errorf("restored %v", b.restored)
	}
	if len(b.purged) != 3 || len(b.purged[0]) != 2 || b.purged[1][0] != "a" || b.purged[2][0] != "b" {
		t.Errorf("purged %v", b.purged)
	}
	// With nothing in the trash only the children show.
	empty := render(t, Trash(TrashProps{Children: p.Children}))
	mustContain(t, empty, "the live list")
	mustNotContain(t, empty, "Empty trash")
}

func TestTrashSelectionActsOnTheTickedRows(t *testing.T) {
	var got [][]string
	del := func(_ context.Context, ids []string) error { got = append(got, ids); return nil }
	var selected []string
	p := TrashProps{Docs: []Doc{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}, Delete: del, Pending: &Pending{Inline: true},
		OnSelect: func(ids []string) { selected = ids }}
	n := Trash(p)
	mustNotContain(t, render(t, n), "gj-trash-restore-selected")
	findHandlers(n, "change")[1](xango.Event{})
	if len(selected) != 1 || selected[0] != "b" {
		t.Fatalf("ticking b: %v", selected)
	}
	p.Selected = selected
	n = Trash(p)
	mustContain(t, render(t, n), "Delete 1 forever")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if len(got) != 2 || len(got[0]) != 1 || got[0][0] != "b" || len(got[1]) != 2 { // the bulk button, then Empty trash
		t.Errorf("deleted %v", got)
	}
}

func TestPendingTracksAnOperation(t *testing.T) {
	changes := 0
	p := NewPending(func() { changes++ })
	p.Inline = true
	p.Run(func(context.Context) error { return errors.New("server said no") })
	if p.Busy() || p.Err() == nil || changes != 2 {
		t.Errorf("busy=%v err=%v changes=%d", p.Busy(), p.Err(), changes)
	}
	html := render(t, DocList(DocListProps{Pending: p}))
	mustContain(t, html, "server said no", `role="alert"`)
	p.Run(func(context.Context) error { return nil })
	if p.Err() != nil {
		t.Error("the next operation clears the error")
	}
	var nilPending *Pending
	if nilPending.Busy() || nilPending.Err() != nil {
		t.Error("a nil Pending is idle")
	}
}

func TestDocListOffersOnlyThePluggedOperations(t *testing.T) {
	docs := []Doc{{ID: "x", Name: "a", Tags: []string{"rpg"}}, {ID: "y", Name: "b"}}
	n := DocList(DocListProps{Docs: docs})
	mustNotContain(t, render(t, n), "Open", "Duplicate", "Trash", "gj-doclist-tags")
	var tagged []string
	n = DocList(DocListProps{Docs: docs, Pending: &Pending{Inline: true}, Current: "y", TagFilter: "rpg", OnTagFilter: func(string) {},
		Tag: func(_ context.Context, id string, tags []string) error {
			tagged = append([]string{id}, tags...)
			return nil
		}})
	html := render(t, n)
	mustContain(t, html, `data-id="x"`, "gj-doclist-tags", "gj-tagchip-on")
	mustNotContain(t, html, `data-id="y"`) // filtered out
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "one, two"})
	}
	if strings.Join(tagged, "|") != "x|one|two" {
		t.Errorf("tagged %v", tagged)
	}
	cur := render(t, DocList(DocListProps{Docs: docs, Current: "y"}))
	mustContain(t, cur, "gj-doclist-current")
}

func TestVersionsListsAndSaves(t *testing.T) {
	var log []string
	p := &Pending{Inline: true}
	n := Versions(VersionsProps{Name: "plan", Pending: p,
		Versions: []DocVersion{{Key: "versions/i/1.json", Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Size: 99}},
		Save:     func(context.Context) error { log = append(log, "save"); return nil },
		Open:     func(_ context.Context, k string) error { log = append(log, "open:"+k); return nil }})
	mustContain(t, render(t, n), "Versions of plan", "2026-01-02 03:04:05", `data-key="versions/i/1.json"`)
	mustNotContain(t, render(t, n), "gj-versions-delete")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if strings.Join(log, " ") != "save open:versions/i/1.json" {
		t.Errorf("log %v", log)
	}
}

func TestDocListShowsFoldersBreadcrumbAndMoves(t *testing.T) {
	var log []string
	p := &Pending{Inline: true}
	props := DocListProps{
		Pending:    p,
		Docs:       []Doc{{ID: "d1", Name: "plan"}},
		Folders:    []DocFolder{{ID: "f2", Name: "2026", Parent: "f1"}},
		Path:       []DocFolder{{ID: "f1", Name: "Work"}},
		AllFolders: []DocFolder{{ID: "f1", Name: "Work"}, {ID: "f2", Name: "2026", Parent: "f1"}},
		OpenFolder: func(_ context.Context, id string) error { log = append(log, "open:"+id); return nil },
		NewFolder:  func(_ context.Context, name string) error { log = append(log, "new:"+name); return nil },
		RenameFolder: func(_ context.Context, id, name string) error {
			log = append(log, "rename:"+id+":"+name)
			return nil
		},
		DeleteFolder: func(_ context.Context, id string) error { log = append(log, "delete:"+id); return nil },
		Move: func(_ context.Context, ids []string, folder string) error {
			log = append(log, "move:"+strings.Join(ids, ",")+":"+folder)
			return nil
		},
		FolderDraft:   "Ideas",
		OnFolderDraft: func(s string) { log = append(log, "draft:"+s) },
	}
	n := DocList(props)
	html := render(t, n)
	mustContain(t, html, "gj-breadcrumbs", ">Documents<", `aria-current="page"`, ">Work<", "📁 2026", `data-id="f2"`,
		"Work / 2026", "Move to…", "gj-doclist-newfolder", `value="Ideas"`)
	mustNotContain(t, html, `value="folder:f1"`) // the folder the document is in is not a place to move it to
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "folder:f1"})
	}
	findHandlers(n, "input")[0](xango.Event{Value: "typed"})
	got := strings.Join(log, " ")
	for _, want := range []string{"open:", "open:f2", "new:Ideas", "delete:f2", "move:d1:f1", "rename:f2:folder:f1", "draft:typed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// The top-level move option and a placeholder that does nothing.
	log = nil
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "top"})
		h(xango.Event{Value: "-"})
	}
	if !strings.Contains(strings.Join(log, " "), "move:d1:") || strings.Count(strings.Join(log, " "), "move:") != 1 {
		t.Errorf("top / placeholder: %v", log)
	}
	// With none of it plugged in there is no breadcrumb, folder or move control.
	bare := render(t, DocList(DocListProps{Docs: props.Docs}))
	mustNotContain(t, bare, "gj-breadcrumbs", "gj-doclist-folder", "gj-doclist-move", "gj-doclist-newfolder")
}
