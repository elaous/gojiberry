package saver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
)

func newSession(t *testing.T) (*Session, *[]docfile.Document, *int) {
	t.Helper()
	var opened []docfile.Document
	changes := 0
	s := NewSession(&Saver{Targets: targets(t)}, func() docfile.Document {
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "body"}}})
	}, func(d docfile.Document) { opened = append(opened, d) })
	s.OnChange = func() { changes++ }
	s.Pending.Inline = true
	return s, &opened, &changes
}

func TestSessionSaveOpenDeleteFlow(t *testing.T) {
	s, opened, changes := newSession(t)
	p := s.Props()
	if p.Target != "sqlite" || p.Format != "json" || len(p.Targets) != 4 || len(p.Formats) != 3 {
		t.Fatalf("%+v", p)
	}
	// Drive it the way the component does.
	p.OnName("report")
	s.Props().OnFormat("proto")
	s.Props().OnSave()
	p = s.Props()
	if p.Failed || p.Status != "report saved" || len(p.Docs.Docs) != 1 || p.Docs.Docs[0].Name != "report" || p.Docs.Docs[0].Format != "Protobuf (binary)" {
		t.Fatalf("after save: %+v", p)
	}
	id := p.Docs.Docs[0].ID
	if !docfile.ValidID(id) || p.Docs.Current != id || s.ID() != id {
		t.Fatalf("the saved document's id %q, current %q", id, p.Docs.Current)
	}
	if *changes == 0 {
		t.Fatal("OnChange never called")
	}
	// Saving again replaces the same document; Save as new makes another with the same name.
	s.Props().OnSave()
	if p = s.Props(); len(p.Docs.Docs) != 1 {
		t.Fatalf("saving twice made %d documents", len(p.Docs.Docs))
	}
	s.Props().OnSaveAsNew()
	if p = s.Props(); len(p.Docs.Docs) != 2 || p.Docs.Docs[0].ID == p.Docs.Docs[1].ID || p.Docs.Docs[0].Name != p.Docs.Docs[1].Name {
		t.Fatalf("save as new: %+v", p.Docs.Docs)
	}
	// Another target starts empty; coming back finds the documents.
	s.Props().OnTarget("memory")
	if p = s.Props(); len(p.Docs.Docs) != 0 || p.Target != "memory" || s.ID() != "" {
		t.Fatalf("%+v", p)
	}
	s.Props().OnTarget("sqlite")
	if p = s.Props(); len(p.Docs.Docs) != 2 {
		t.Fatalf("%+v", p)
	}
	// Opening hands over the document and adopts its id, name and format.
	s.SetName("other")
	s.SetFormat("json")
	if err := p.Docs.Open(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 || (*opened)[0].Title != "report" || s.Name() != "report" || s.ID() != id || s.Props().Format != "proto" || s.Props().Status != "report opened" {
		t.Fatalf("%+v %q %+v", *opened, s.Name(), s.Props())
	}
	// Detach: what is in the editor is not that document any more.
	s.Detach()
	if s.ID() != "" {
		t.Fatal("Detach")
	}
	if err := s.Props().Docs.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if p = s.Props(); len(p.Docs.Docs) != 1 || p.Status != "report deleted" {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(s.String(), "sqlite") {
		t.Fatal(s.String())
	}
}

func TestSessionErrors(t *testing.T) {
	ctx := context.Background()
	s, opened, _ := newSession(t)
	s.Props().OnSave() // no name
	if p := s.Props(); !p.Failed || p.Busy || !strings.Contains(p.Status, "name") {
		t.Fatalf("%+v", p)
	}
	s.SetName("x")
	s.SetFormat("nope")
	s.Props().OnSave()
	if p := s.Props(); !p.Failed || !strings.Contains(p.Status, "no format") {
		t.Fatalf("%+v", p)
	}
	if err := s.Open(ctx, docfile.NewID()); err == nil || len(*opened) != 0 {
		t.Fatal("opening a missing document fails")
	}
	if err := s.Delete(ctx, docfile.NewID()); err == nil {
		t.Fatal("delete of a missing document fails")
	}
	if err := s.SetTarget(ctx, "nope"); err == nil {
		t.Fatal("listing an unknown target fails")
	}
	if err := s.SaveVersion(ctx); err == nil || !strings.Contains(err.Error(), "save the document") {
		t.Fatalf("a version of an unsaved document: %v", err)
	}
	// A failed operation shows on the component, and the next one clears it.
	_ = s.SetTarget(ctx, "memory")
	s.Props().Docs.Pending.Run(func(ctx context.Context) error { return s.Delete(ctx, docfile.NewID()) })
	if p := s.Props(); !p.Failed || p.Status == "" {
		t.Fatalf("%+v", p)
	}
	// Defaults: no OnChange, no OnOpen.
	bare := NewSession(&Saver{}, func() docfile.Document { return docfile.Document{} }, nil)
	bare.SetName("n")
	if err := bare.Open(ctx, "x"); err == nil {
		t.Fatal("no targets")
	}
	if bare.Props().Target != "" {
		t.Fatal("no targets, no target")
	}
}

func TestSessionRealGoroutine(t *testing.T) {
	s, _, _ := newSession(t)
	s.Pending.Inline = false
	got := make(chan struct{}, 16)
	s.OnChange = func() { got <- struct{}{} }
	s.Props().Docs.Refresh()
	<-got
	<-got // busy, then done
}

func TestSessionTouchesTheEditorThroughOnUI(t *testing.T) {
	ctx := context.Background()
	s, opened, _ := newSession(t)
	calls := 0
	s.OnUI = func(f func()) { calls++; f() }
	s.SetName("doc")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Save read the editor %d times through OnUI, want 1", calls)
	}
	if err := s.Open(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(*opened) != 1 {
		t.Fatalf("Open delivered the document through OnUI: calls=%d opened=%d", calls, len(*opened))
	}
}

func TestSessionPlugsIntoTheDocumentComponents(t *testing.T) {
	ctx := context.Background()
	s, opened, _ := newSession(t)
	s.SetName("report")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	// Versions of the saved document.
	if err := s.Props().Versions.Save(ctx); err != nil {
		t.Fatal(err)
	}
	v := s.Versions()
	if v.Name != "report" || len(v.Versions) != 1 || s.Props().Status != "A version of report saved" {
		t.Fatalf("after a version: %+v", v)
	}
	if err := v.Open(ctx, v.Versions[0].Key); err != nil || len(*opened) != 1 || s.Props().Status != "Version opened" {
		t.Fatalf("opening a version: %d opened, %q, %v", len(*opened), s.Props().Status, err)
	}
	if err := v.Delete(ctx, v.Versions[0].Key); err != nil || len(s.Versions().Versions) != 0 {
		t.Error("version not deleted")
	}

	// Two documents with one name, tagged.
	first := s.ID()
	s.Props().OnSaveAsNew()
	second := s.ID()
	if first == second || first == "" {
		t.Fatalf("ids %q %q", first, second)
	}
	docs := s.Docs()
	if err := docs.Tag(ctx, first, []string{"rpg", "draft"}); err != nil {
		t.Fatal(err)
	}
	if err := docs.Tag(ctx, second, []string{"draft"}); err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, d := range s.Docs().Docs {
		tags = append(tags, d.Tags...)
	}
	if len(tags) != 3 {
		t.Fatalf("tags %v", tags)
	}
	s.Docs().OnTagFilter("rpg")
	if got := s.Docs().TagFilter; got != "rpg" {
		t.Errorf("filter %q", got)
	}
	if err := docs.Tag(ctx, first, nil); err != nil { // the last "rpg" goes: the filter must not strand the list
		t.Fatal(err)
	}
	if got := s.Docs().TagFilter; got != "" {
		t.Errorf("after the last rpg tag went: %q", got)
	}

	// Duplicate, trash, restore, purge: the trash as a plug-in backend.
	if err := docs.Duplicate(ctx, first); err != nil {
		t.Fatal(err)
	}
	if p := s.Props(); len(p.Docs.Docs) != 3 || p.Status != "Copy of report created" {
		t.Fatalf("duplicate: %d docs, %q", len(p.Docs.Docs), p.Status)
	}
	if err := docs.Trash(ctx, []string{first}); err != nil {
		t.Fatal(err)
	}
	tr := s.Trash()
	if len(s.Docs().Docs) != 2 || len(tr.Docs) != 1 || tr.Docs[0].ID != first || tr.Docs[0].Name != "report" {
		t.Fatalf("trash: %+v", tr)
	}
	if err := tr.Restore(ctx, []string{first}); err != nil {
		t.Fatal(err)
	}
	if p := s.Props(); len(p.Docs.Docs) != 3 || len(p.Trash.Docs) != 0 || p.Status != "report restored" {
		t.Fatalf("restore: %d docs, %d trashed, %q", len(p.Docs.Docs), len(p.Trash.Docs), p.Status)
	}
	_ = docs.Trash(ctx, []string{second})
	if err := s.Trash().Delete(ctx, []string{second}); err != nil {
		t.Fatal(err)
	}
	if p := s.Props(); len(p.Trash.Docs) != 0 || len(p.Docs.Docs) != 2 {
		t.Fatalf("purge: %+v", p)
	}
	// The trash's selection.
	_ = docs.Trash(ctx, []string{first})
	trash := s.Trash()
	trash.OnSelect([]string{first, "gone"})
	if got := s.Trash().Selected; !reflect.DeepEqual(got, []string{first, "gone"}) {
		t.Errorf("selected %v", got)
	}
	_ = s.Refresh(ctx) // a selection of something no longer in the trash is dropped
	if got := s.Trash().Selected; !reflect.DeepEqual(got, []string{first}) {
		t.Errorf("after refresh %v", got)
	}
	// Trashing something that is not there fails and shows.
	s.Pending.Run(func(ctx context.Context) error { return s.TrashDocs(ctx, []string{docfile.NewID()}) })
	if p := s.Props(); !p.Failed {
		t.Error("a failure must show")
	}
	var _ components.TrashBackend = s
}

func TestSessionFoldersAndBreadcrumb(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSession(t)
	s.SetName("plan")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	rootDoc := s.ID()
	docs := s.Docs()
	if len(docs.Docs) != 1 || len(docs.Path) != 0 || docs.OpenFolder == nil {
		t.Fatalf("%+v", docs)
	}
	// Make Work, go in; a document saved there is in it, and the root list no longer shows it.
	if err := docs.NewFolder(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	work := s.Docs().Folders[0]
	if err := s.Docs().OpenFolder(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	s.Detach()
	s.SetName("inside")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	inside := s.ID()
	d := s.Docs()
	if len(d.Docs) != 1 || d.Docs[0].ID != inside || len(d.Path) != 1 || d.Path[0].ID != work.ID {
		t.Fatalf("in Work: %+v", d)
	}
	if err := d.NewFolder(ctx, "2026"); err != nil {
		t.Fatal(err)
	}
	if got := s.Docs(); len(got.Folders) != 1 || got.Folders[0].Name != "2026" || len(got.AllFolders) != 2 {
		t.Fatalf("%+v", got)
	}
	// Move the root document in; opening a document goes to its folder.
	if err := s.Docs().Move(ctx, []string{rootDoc}, work.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Docs(); len(got.Docs) != 2 {
		t.Fatalf("after the move: %+v", got.Docs)
	}
	if err := s.OpenFolder(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(ctx, inside); err != nil {
		t.Fatal(err)
	}
	if got := s.Docs(); len(got.Path) != 1 || got.Path[0].ID != work.ID {
		t.Errorf("opening a document should show its folder: %+v", got.Path)
	}
	// A folder with documents cannot be deleted; an empty one can, and the view moves up.
	if err := s.Docs().DeleteFolder(ctx, work.ID); err == nil {
		t.Error("deleted a folder with documents")
	}
	sub := s.Docs().Folders[0]
	_ = s.OpenFolder(ctx, sub.ID)
	if err := s.Docs().DeleteFolder(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Docs(); len(got.Path) != 1 || got.Path[0].ID != work.ID || len(got.Folders) != 0 {
		t.Errorf("after deleting the folder being shown: %+v", got)
	}
	if err := s.OpenFolder(ctx, docfile.NewID()); err == nil {
		t.Error("opened a folder that is not there")
	}
	// Changing target starts at the top.
	_ = s.SetTarget(ctx, "memory")
	if got := s.Docs(); len(got.Path) != 0 || len(got.Folders) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestOnlyTheFormAsksBeforeDeleting(t *testing.T) {
	s, _, _ := newSession(t)
	if s.Docs().Confirm != nil || s.Trash().Confirm != nil {
		t.Error("Docs and Trash used alone would store a question nothing draws: they delete at once")
	}
	p := s.Props()
	if p.Docs.Confirm == nil || p.Trash.Confirm == nil || p.Asker == nil {
		t.Error("the Saver form draws the dialog, so it asks")
	}
}

func TestStatusLinesNameWhatTheyAreAbout(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSession(t)
	s.SetName("plan")
	_ = s.Save(ctx)
	id := s.ID()
	status := func() string { return s.Props().Status }
	_ = s.NewFolder(ctx, "Work")
	if status() != "Work created" {
		t.Errorf("%q", status())
	}
	work := s.Docs().Folders[0]
	_ = s.Docs().Move(ctx, []string{id}, work.ID)
	if status() != "plan moved" {
		t.Errorf("%q", status())
	}
	_ = s.RenameFolder(ctx, work.ID, "Job")
	if status() != "Job renamed" {
		t.Errorf("%q", status())
	}
	_ = s.Docs().Trash(ctx, []string{id})
	if status() != "plan moved to the trash" {
		t.Errorf("%q", status())
	}
	_ = s.Trash().Restore(ctx, []string{id})
	if status() != "plan restored" {
		t.Errorf("%q", status())
	}
	_ = s.Docs().Move(ctx, []string{id}, "")
	_ = s.DeleteFolder(ctx, work.ID)
	if status() != "Job deleted" {
		t.Errorf("%q", status())
	}
	_ = s.Docs().Tag(ctx, id, []string{"a"})
	if status() != "Tags of plan updated" {
		t.Errorf("%q", status())
	}
	_ = s.Docs().Duplicate(ctx, id)
	if status() != "Copy of plan created" {
		t.Errorf("%q", status())
	}
	ids := []string{}
	for _, d := range s.Docs().Docs {
		ids = append(ids, d.ID)
	}
	_ = s.Docs().Trash(ctx, ids)
	if status() != "plan, Copy of plan moved to the trash" && status() != "Copy of plan, plan moved to the trash" {
		t.Errorf("two names: %q", status())
	}
}
