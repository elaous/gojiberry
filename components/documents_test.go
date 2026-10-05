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

func TestCapacityShowsPercentUsedAndLeft(t *testing.T) {
	html := render(t, Capacity(CapacityProps{Used: 25 << 20, Total: 100 << 20, ShowCounts: true, Documents: 3, Trashed: 1}))
	mustContain(t, html, `role="meter"`, `aria-valuenow="25"`, ">25%<", "25.0 MB of 100.0 MB used", "75.0 MB left", "width:25.0%", "3 documents", "1 in the trash")
	mustNotContain(t, html, "gj-capacity-high", "gj-capacity-over")
	mustContain(t, render(t, Capacity(CapacityProps{Used: 95, Total: 100})), "gj-capacity-high", "5 B left")
	over := render(t, Capacity(CapacityProps{Used: 150, Total: 100}))
	mustContain(t, over, "gj-capacity-over", "width:100.0%", "0 B left", ">150%<")
	unknown := render(t, Capacity(CapacityProps{Used: 2048, Label: "Disk"}))
	mustContain(t, unknown, "2.0 KB used", "allocated space not known", "Disk")
	mustNotContain(t, unknown, `role="meter"`, "documents")
}

func TestDocListLinkAndDownload(t *testing.T) {
	docs := []Doc{{ID: "x", Name: "a"}}
	var copied, got []string
	p := DocListProps{Docs: docs, Folders: []DocFolder{{ID: "f", Name: "Work"}}, AllFolders: []DocFolder{{ID: "f", Name: "Work"}},
		Pending: &Pending{Inline: true},
		Link:    func(id string) string { return "https://opendocs.io/d/" + id }, FolderLink: func(id string) string { return "https://opendocs.io/f/" + id },
		CopyLink:       func(u string) { copied = append(copied, u) },
		Download:       func(_ context.Context, id, format string) error { got = append(got, id+":"+format); return nil },
		DownloadFolder: func(_ context.Context, id string) error { got = append(got, "folder:"+id); return nil }}
	html := render(t, DocList(p))
	mustContain(t, html, `data-url="https://opendocs.io/d/x"`, `data-url="https://opendocs.io/f/f"`, "gj-doclist-download", "gj-doclist-folder-download")
	mustNotContain(t, html, "Download as")
	n := DocList(p)
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if strings.Join(copied, " ") != "https://opendocs.io/f/f https://opendocs.io/d/x" {
		t.Errorf("copied %v", copied)
	}
	if strings.Join(got, " ") != "folder:f x:" {
		t.Errorf("downloads %v", got)
	}

	// With formats, a menu instead of a button.
	got = nil
	p.DownloadFormats = []DocChoice{{Name: "txt", Label: "Text"}}
	n = DocList(p)
	mustContain(t, render(t, n), "Download as…", `value="format:txt"`, ">Text<")
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "-"})
		h(xango.Event{Value: "format:txt"})
	}
	if strings.Join(got, " ") != "x:txt" {
		t.Errorf("menu: %v", got)
	}

	// Left out when not plugged in.
	mustNotContain(t, render(t, DocList(DocListProps{Docs: docs})), "gj-doclist-link", "gj-doclist-download")
	mustNotContain(t, render(t, DocList(DocListProps{Docs: docs, Link: p.Link})), "gj-doclist-link") // nowhere to copy to
}

func TestMigratePanel(t *testing.T) {
	var log []string
	p := MigrateProps{
		Sources: []DocChoice{{Name: "google", Label: "Google Docs"}}, OnSource: func(n string) { log = append(log, "source:"+n) },
		OnToken: func(s string) { log = append(log, "token:"+s) },
	}
	html := render(t, Migrate(p))
	mustContain(t, html, "Choose a platform…", `value="google"`)
	mustNotContain(t, html, "gj-migrate-token", "gj-migrate-list", "gj-migrate-pick") // no platform yet, not connected, no file picker

	p.Formats, p.Format, p.OnFormat = []DocChoice{{Name: "open", Label: "opendocs (.open)"}, {Name: "json", Label: "JSON"}}, "open", func(n string) { log = append(log, "format:"+n) }
	mustContain(t, render(t, Migrate(p)), "Convert to", `value="open" selected="selected"`, ">JSON<")
	p.Source, p.Token = "google", "tok"
	p.OnConnect = func() { log = append(log, "connect") }
	n := Migrate(p)
	mustContain(t, render(t, n), "gj-migrate-token", `value="tok"`, "gj-migrate-connect")
	mustNotContain(t, render(t, n), "gj-migrate-list")
	p.Token = " "
	mustContain(t, render(t, Migrate(p)), `class="gj-migrate-connect" disabled="disabled"`)

	p.Token, p.Connected = "tok", true
	p.Path = []MigrateItem{{ID: "f1", Name: "Work"}}
	p.Items = []MigrateItem{{ID: "f2", Name: "Sub", Folder: true}, {ID: "d1", Name: "Plan", Meta: "2026-01-02", Selected: true}}
	p.OnOpenFolder = func(id string) { log = append(log, "open:"+id) }
	p.OnToggle = func(id string) { log = append(log, "toggle:"+id) }
	p.OnSelectAll = func() { log = append(log, "all") }
	p.OnStart = func() { log = append(log, "start") }
	p.OnPickFiles = func() { log = append(log, "pick") }
	n = Migrate(p)
	html = render(t, n)
	mustContain(t, html, "All files", "Work", "📁 Sub", "Plan", "2026-01-02", `data-id="d1"`, `checked="checked"`, "Select all", "Import Word files…")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "google"})
	}
	joined := strings.Join(log, " ")
	for _, want := range []string{"open:", "open:f2", "all", "start", "pick", "toggle:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q never happened: %s", want, joined)
		}
	}

	// Progress while it runs, the report after.
	p.Busy, p.Done, p.Total, p.Current = true, 2, 5, "Plan.docx"
	mustContain(t, render(t, Migrate(p)), "<progress", "2 of 5: Plan.docx")
	p.Busy, p.Reported, p.Imported, p.Failed = false, true, 4, []MigrateFailure{{Name: "Bad", Err: "not a Word file"}}
	html = render(t, Migrate(p))
	mustContain(t, html, "4 documents migrated", "1 could not be migrated:", "Bad: not a Word file")
	mustNotContain(t, html, "<progress")
	p.Error = "boom"
	mustContain(t, render(t, Migrate(p)), `role="alert"`, "boom")
}
