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

func TestRecipientsListAddWithAConfirmedFingerprintAndRemove(t *testing.T) {
	var added, removed []string
	var draft, confirm string
	p := RecipientsProps{
		Owner:   []RecipientRow{{Label: "ana@opendocs.io", Type: "ssh-ed25519", Fingerprint: "SHA256:owner"}},
		Shared:  []RecipientRow{{Label: "bo", Type: "ssh-ed25519", Fingerprint: "SHA256:bo"}},
		Pending: &Pending{Inline: true},
		OnDraft: func(k, c string) { draft, confirm = k, c },
		Add:     func(_ context.Context, k, c string) error { added = append(added, k+"|"+c); return nil },
		Remove:  func(_ context.Context, fp string) error { removed = append(removed, fp); return nil },
		Ask:     func(msg string, do func()) { mustContain(t, msg, "Stop sharing with bo", "cannot be taken back"); do() },
	}
	html := render(t, Recipients(p))
	mustContain(t, html, "Keys that can open this document", "ana@opendocs.io", "owner", "SHA256:owner", "SHA256:bo", "gj-recip-remove", "gj-recip-key", "Add key")
	mustNotContain(t, html, "gj-recip-confirm") // nothing parsed yet

	p.Draft, p.ParseError = "nonsense", "not an SSH public key"
	mustContain(t, render(t, Recipients(p)), "gj-recip-error", `role="alert"`, "not an SSH public key")

	// A parsed key asks for its fingerprint; Add is disabled until it is typed exactly.
	p.Draft, p.ParseError = "ssh-ed25519 AAAA cy", ""
	p.Parsed = &RecipientRow{Label: "cy", Type: "ssh-ed25519", Fingerprint: "SHA256:cy"}
	html = render(t, Recipients(p))
	mustContain(t, html, "ssh-ed25519 key for cy", "SHA256:cy", "Check this fingerprint with the person", "gj-recip-confirm")
	if disabledAdd(t, p) != true {
		t.Error("Add is off before the fingerprint is confirmed")
	}
	p.Confirm = "SHA256:other"
	if disabledAdd(t, p) != true {
		t.Error("a different fingerprint does not confirm")
	}
	p.Confirm = " SHA256:cy "
	if disabledAdd(t, p) {
		t.Error("the fingerprint typed confirms (spaces around it do not matter)")
	}
	for _, h := range findHandlers(Recipients(p), "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if len(added) != 1 || added[0] != "ssh-ed25519 AAAA cy| SHA256:cy " || len(removed) != 1 || removed[0] != "SHA256:bo" {
		t.Errorf("%v %v", added, removed)
	}
	for _, h := range findHandlers(Recipients(p), "input") {
		h(xango.NewEvent("input", "typed", false, nil, nil))
	}
	if draft == "" && confirm == "" {
		t.Error("typing is reported")
	}
	// Without Add and Remove there are no controls.
	html = render(t, Recipients(RecipientsProps{Shared: p.Shared, Pending: p.Pending}))
	mustNotContain(t, html, "gj-recip-remove", "gj-recip-form")
}

func disabledAdd(t *testing.T, p RecipientsProps) bool {
	t.Helper()
	html := render(t, Recipients(p))
	i := strings.Index(html, `class="gj-recip-add`)
	if i < 0 {
		t.Fatal("no Add button")
	}
	j := strings.Index(html[i:], ">")
	return strings.Contains(html[i:i+j], "disabled")
}

func TestPasskeyControlStates(t *testing.T) {
	var did []string
	p := PasskeyProps{Supported: true, Pending: &Pending{Inline: true}, Email: "ana@opendocs.io",
		SignIn: func(context.Context) error { did = append(did, "signin"); return nil },
		Create: func(_ context.Context, e, _ string) error { did = append(did, "create:"+e); return nil },
		Add:    func(_ context.Context, l string) error { did = append(did, "add:"+l); return nil },
		Remove: func(_ context.Context, id string) error { did = append(did, "remove:"+id); return nil },
		Ask: func(msg string, do func()) {
			mustContain(t, msg, "Remove the passkey Laptop", "only way to sign in")
			do()
		},
		Label: "Phone",
		Rows:  []PasskeyRow{{ID: "c1", Label: "Laptop", Created: "2026-10-06", LastUsed: "2026-10-07", Synced: true}}}
	html := render(t, Passkey(p))
	mustContain(t, html, "Sign in with a passkey", "Create an account with a passkey", "two factors in one", "gj-passkey-email")
	mustNotContain(t, html, "gj-passkey-item", "Your passkeys")
	for _, h := range findHandlers(Passkey(p), "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if len(did) != 2 || did[0] != "signin" || did[1] != "create:ana@opendocs.io" {
		t.Errorf("%v", did)
	}
	// Create needs an address.
	p.Email = ""
	html = render(t, Passkey(p))
	if i := strings.Index(html, `class="gj-passkey-create"`); i < 0 || !strings.Contains(html[i:i+strings.Index(html[i:], ">")], "disabled") {
		t.Error("Create is off with no e-mail address")
	}
	did = nil
	p.SignedIn = true
	html = render(t, Passkey(p))
	mustContain(t, html, "Your passkeys", "Laptop", "added 2026-10-06", "last used 2026-10-07", "synced across your devices", "gj-passkey-remove", "Add a passkey")
	mustNotContain(t, html, "gj-passkey-signin")
	for _, h := range findHandlers(Passkey(p), "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if len(did) != 2 || did[0] != "remove:c1" || did[1] != "add:Phone" {
		t.Errorf("%v", did)
	}
	mustContain(t, render(t, Passkey(PasskeyProps{Pending: &Pending{Inline: true}})), "cannot use passkeys")
}

func TestRecipientsRemoveEverywhereWarnsAndCountsDocuments(t *testing.T) {
	var removed []string
	var asked string
	p := RecipientsProps{
		Shared:           []RecipientRow{{Label: "bo", Type: "ssh-ed25519", Fingerprint: "SHA256:bo", Uses: 3}, {Label: "cy", Type: "ssh-ed25519", Fingerprint: "SHA256:cy", Uses: 1}},
		Pending:          &Pending{Inline: true},
		RemoveEverywhere: func(_ context.Context, fp string) error { removed = append(removed, fp); return nil },
		Ask:              func(msg string, do func()) { asked = msg; do() },
	}
	html := render(t, Recipients(p))
	mustContain(t, html, "gj-recip-remove-all", "Remove from all 3 documents", "opens 3 documents")
	if strings.Count(html, "gj-recip-remove-all") != 1 {
		t.Error("only a key used on several documents gets the button")
	}
	mustNotContain(t, html, "gj-recip-remove\"") // no single Remove without the operation
	for _, h := range findHandlers(Recipients(p), "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	mustContain(t, asked, "bo can open 3 documents", "decrypts every one", "cannot be taken back")
	if len(removed) != 1 || removed[0] != "SHA256:bo" {
		t.Errorf("%v", removed)
	}
}
