package saver

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
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
	if p.Target != "sqlite" || p.Format != "json" || len(p.Targets) != 4 || len(p.Formats) != 4 {
		t.Fatalf("%+v", p)
	}
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
	s.Props().OnSave()
	if p = s.Props(); len(p.Docs.Docs) != 1 {
		t.Fatalf("saving twice made %d documents", len(p.Docs.Docs))
	}
	s.Props().OnSaveAsNew()
	if p = s.Props(); len(p.Docs.Docs) != 2 || p.Docs.Docs[0].ID == p.Docs.Docs[1].ID || p.Docs.Docs[0].Name != p.Docs.Docs[1].Name {
		t.Fatalf("save as new: %+v", p.Docs.Docs)
	}
	s.Props().OnTarget("memory")
	if p = s.Props(); len(p.Docs.Docs) != 0 || p.Target != "memory" || s.ID() != "" {
		t.Fatalf("%+v", p)
	}
	s.Props().OnTarget("sqlite")
	if p = s.Props(); len(p.Docs.Docs) != 2 {
		t.Fatalf("%+v", p)
	}
	s.SetName("other")
	s.SetFormat("json")
	if err := p.Docs.Open(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 || (*opened)[0].Title != "report" || s.Name() != "report" || s.ID() != id || s.Props().Format != "proto" || s.Props().Status != "report opened" {
		t.Fatalf("%+v %q %+v", *opened, s.Name(), s.Props())
	}
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
	_ = s.SetTarget(ctx, "memory")
	s.Props().Docs.Pending.Run(func(ctx context.Context) error { return s.Delete(ctx, docfile.NewID()) })
	if p := s.Props(); !p.Failed || p.Status == "" {
		t.Fatalf("%+v", p)
	}
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
	if err := docs.Tag(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Docs().TagFilter; got != "" {
		t.Errorf("after the last rpg tag went: %q", got)
	}

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
	_ = docs.Trash(ctx, []string{first})
	trash := s.Trash()
	trash.OnSelect([]string{first, "gone"})
	if got := s.Trash().Selected; !reflect.DeepEqual(got, []string{first, "gone"}) {
		t.Errorf("selected %v", got)
	}
	_ = s.Refresh(ctx)
	if got := s.Trash().Selected; !reflect.DeepEqual(got, []string{first}) {
		t.Errorf("after refresh %v", got)
	}
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

func TestSessionDownloadsAndLinks(t *testing.T) {
	s, _, _ := newSession(t)
	s.Props().OnName("report")
	s.Props().OnSave()
	id := s.Props().Docs.Docs[0].ID
	if p := s.Docs(); p.Download != nil || p.Link != nil {
		t.Fatal("no Deliver and no Link: the controls are left out")
	}

	type file struct {
		name, mime string
		data       []byte
	}
	var got []file
	var copied []string
	s.Deliver = func(name, mime string, data []byte) { got = append(got, file{name, mime, data}) }
	s.Copy = func(text string) { copied = append(copied, text) }
	s.Link = func(kind, id string) string { return "https://opendocs.io/" + kind + "/" + id }
	s.Export = func(format string, d docfile.Document) (string, []byte, string, error) {
		return d.Name() + "." + format, []byte("as " + format), "text/plain", nil
	}
	s.DownloadFormats = []components.SaverChoice{{Name: "txt", Label: "Text"}}

	p := s.Docs()
	if p.Download == nil || p.DownloadFolder == nil || len(p.DownloadFormats) != 1 || p.Link(id) != "https://opendocs.io/document/"+id || p.FolderLink("f") != "https://opendocs.io/folder/f" {
		t.Fatalf("%+v", p)
	}
	ctx := context.Background()
	if err := s.Download(ctx, id, "txt"); err != nil || len(got) != 1 || got[0].name != "report.txt" || string(got[0].data) != "as txt" {
		t.Fatalf("%v %+v", err, got)
	}
	if s.Props().Status != "report downloaded" {
		t.Errorf("status %q", s.Props().Status)
	}
	if err := s.Download(ctx, id, ""); err != nil || got[1].name != "report.json" || !strings.Contains(string(got[1].data), "report") {
		t.Fatalf("%v %+v", err, got)
	}
	if err := s.DownloadFolder(ctx, ""); err != nil || got[2].name != "documents.zip" || got[2].mime != "application/zip" || len(zipNames(t, got[2].data)) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	s.CopyLink("https://opendocs.io/document/" + id)
	if len(copied) != 1 || !strings.HasPrefix(s.Props().Status, "Link copied: ") {
		t.Errorf("%v %q", copied, s.Props().Status)
	}
	if err := s.Download(ctx, docfile.NewID(), ""); err == nil {
		t.Error("a document that is not there")
	}
}

func TestSessionSaveState(t *testing.T) {
	s, _, _ := newSession(t)
	if s.SaveState() != "" {
		t.Fatal(s.SaveState())
	}
	s.MarkEdited()
	if s.SaveState() != SaveUnsaved {
		t.Fatal(s.SaveState())
	}
	s.Props().OnName("report")
	s.Props().OnSave()
	if s.SaveState() != SaveSaved {
		t.Fatal(s.SaveState())
	}
	s.MarkEdited()
	if s.SaveState() != SaveUnsaved {
		t.Fatal(s.SaveState())
	}
	s.Get = func() docfile.Document {
		s.MarkEdited()
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "body"}}})
	}
	var during []string
	s.OnChange = func() { during = append(during, s.SaveState()) }
	s.Props().OnSave()
	if !slices.Contains(during, SaveSaving) || s.SaveState() != SaveUnsaved {
		t.Errorf("during %q after %q", during, s.SaveState())
	}
	s.Props().OnName("")
	s.Props().OnSave()
	if s.SaveState() != SaveFailed {
		t.Errorf("%q", s.SaveState())
	}
	id := s.Docs().Docs[0].ID
	s.Pending.Run(func(ctx context.Context) error { return s.Open(ctx, id) })
	if s.SaveState() != SaveSaved {
		t.Errorf("%q", s.SaveState())
	}
}

func TestSaveStateAfterTheOpenDocumentIsDeletedOrTrashed(t *testing.T) {
	s, _, _ := newSession(t)
	s.Props().OnName("one")
	s.Props().OnSave()
	id := s.Docs().Docs[0].ID
	if s.SaveState() != SaveSaved {
		t.Fatal(s.SaveState())
	}
	if err := s.TrashDocs(context.Background(), []string{id}); err != nil {
		t.Fatal(err)
	}
	if s.SaveState() != SaveUnsaved {
		t.Errorf("the open document went to the trash, so the editor's text is stored nowhere: %q", s.SaveState())
	}
	s.Props().OnSave() // a new document
	id2 := s.Docs().Docs[0].ID
	if s.SaveState() != SaveSaved {
		t.Fatal(s.SaveState())
	}
	if err := s.Delete(context.Background(), id2); err != nil {
		t.Fatal(err)
	}
	if s.SaveState() != SaveUnsaved {
		t.Errorf("deleted: %q", s.SaveState())
	}
}

func TestOpenDownloadAsksWhetherToKeepTheHistory(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSession(t)
	var files []string
	var sizes []int
	s.Deliver = func(name, _ string, data []byte) { files = append(files, name); sizes = append(sizes, len(data)) }
	s.SetName("report")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVersion(ctx); err != nil {
		t.Fatal(err)
	}
	id := s.ID()
	if err := s.Download(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("it asks first")
	}
	od := s.Docs().OpenDownload
	if od == nil || od.Versions != 1 || od.Current == 0 || od.History == 0 || od.Drop {
		t.Fatalf("%+v", od)
	}
	od.OnDrop(true)
	if !s.Docs().OpenDownload.Drop {
		t.Fatal("the checkbox is kept")
	}
	s.Docs().OpenDownload.OnConfirm()
	if len(files) != 1 || files[0] != "report.open" || int64(sizes[0]) != od.Current || s.Docs().OpenDownload != nil {
		t.Fatalf("%v %v", files, sizes)
	}
	_ = s.Download(ctx, id, "open")
	s.Docs().OpenDownload.OnConfirm()
	if int64(sizes[1]) != od.Current+od.History {
		t.Errorf("%d against %d + %d", sizes[1], od.Current, od.History)
	}
	_ = s.Download(ctx, id, "open")
	s.Docs().OpenDownload.OnCancel()
	if s.Docs().OpenDownload != nil || len(files) != 2 {
		t.Error("cancel closes the dialog and downloads nothing")
	}
}

func TestSessionPackDownloadAndImport(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSession(t)
	var file string
	var data []byte
	s.Deliver = func(name, _ string, d []byte) { file, data = name, d }
	f, _ := s.Saver.CreateFolder(ctx, s.targetName(), "", "Work")
	s.SetName("report")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveDocs(ctx, []string{s.ID()}, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DownloadPack(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if file != "Work.opendoc" || len(data) < 100 || !strings.Contains(s.Props().Status, "a pack of 1 documents") {
		t.Fatalf("%q %d %q", file, len(data), s.Props().Status)
	}
	other, _, _ := newSession(t)
	if err := other.ImportPackData(ctx, "Work.opendoc", data); err != nil {
		t.Fatal(err)
	}
	if st := other.Props().Status; !strings.Contains(st, "1 documents and 1 folders imported") {
		t.Fatalf("%q", st)
	}
	if err := other.ImportPackData(ctx, "Work.opendoc", data); err != nil {
		t.Fatal(err)
	}
	if st := other.Props().Status; !strings.Contains(st, "0 documents") || !strings.Contains(st, "1 already here") {
		t.Fatalf("a second import skips: %q", st)
	}
	if err := other.ImportPackData(ctx, "bad.opendoc", data[:len(data)/2]); err == nil {
		t.Error("a cut pack")
	}
	p := s.Docs()
	if p.DownloadPack == nil || p.ImportPack != nil {
		t.Error("download needs Deliver, import needs Pick")
	}
	s.Pick = func(string, func(string, []byte, error)) {}
	if s.Docs().ImportPack == nil {
		t.Error("Pick turns the import button on")
	}
}

func TestSessionWarnsWhenTheStoreIsFillingUpAndExplainsAFullOne(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := NewSession(&Saver{Targets: []Target{{Name: "m", Adapter: mem, Capacity: 1200}}}, func() docfile.Document {
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: strings.Repeat("x", 800)}}})
	}, nil)
	s.Pending.Inline = true
	s.SetName("big")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.Props().Status; !strings.Contains(st, "big saved") || !strings.Contains(st, "Storage is") || !strings.Contains(st, "full") {
		t.Errorf("a save into a filling store says so: %q", st)
	}

	full := NewSession(&Saver{Targets: []Target{{Name: "m", Adapter: &capped{Adapter: docstore.NewMemoryAdapter(), room: 10}}}}, func() docfile.Document {
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "text"}}})
	}, nil)
	full.Pending.Inline = true
	full.SetName("x")
	err := full.Save(ctx)
	if err == nil || !strings.Contains(err.Error(), "Not saved") || !strings.Contains(err.Error(), "Download the document") || !errors.Is(err, docstore.ErrStorageFull) {
		t.Fatalf("%v", err)
	}
}

func TestCompareVersionShowsWhatChangedSinceASnapshot(t *testing.T) {
	ctx := context.Background()
	text := "First paragraph\nSecond paragraph\n"
	s := NewSession(&Saver{Targets: targets(t)}, func() docfile.Document {
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: text}}})
	}, func(docfile.Document) {})
	s.Pending.Inline = true
	s.SetName("report")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVersion(ctx); err != nil {
		t.Fatal(err)
	}
	vs := s.Versions()
	if len(vs.Versions) != 1 || vs.Compare == nil || vs.Diff != nil || vs.DiffKey != "" {
		t.Fatalf("before comparing: %+v", vs)
	}
	key := vs.Versions[0].Key
	if err := s.CompareVersion(ctx, key); err != nil {
		t.Fatal(err)
	}
	if v := s.Versions(); v.DiffKey != key || docdiff.Summarize(v.Diff).Changed() {
		t.Fatalf("unchanged: %+v", v)
	}
	text = "First paragraph\nA new second paragraph\nThird\n"
	if err := s.CompareVersion(ctx, key); err != nil {
		t.Fatal(err)
	}
	sum := docdiff.Summarize(s.Versions().Diff)
	if !sum.Changed() || sum.Inserted+sum.Edited == 0 {
		t.Fatalf("after editing: %+v", sum)
	}
	if err := s.CompareVersion(ctx, "versions/"+s.ID()+"/nope"); err == nil {
		t.Error("a snapshot that is not there is an error")
	}
	if err := s.CompareVersion(ctx, "../etc/passwd"); err == nil {
		t.Error("a key that is not a version's is refused")
	}
	if err := s.DeleteVersion(ctx, key); err != nil {
		t.Fatal(err)
	}
	if v := s.Versions(); v.DiffKey != "" || v.Diff != nil {
		t.Errorf("after deleting: %+v", v)
	}
}

func TestRestoreVersionKeepsWhatWasBeingEdited(t *testing.T) {
	ctx := context.Background()
	text := "original text\n"
	var opened []docfile.Document
	s := NewSession(&Saver{Targets: targets(t)}, func() docfile.Document {
		return docfile.FromFlow("", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: text}}})
	}, func(d docfile.Document) { opened = append(opened, d) })
	s.Pending.Inline = true
	s.Author = "ana@example.com"
	if err := s.RestoreVersion(ctx, "versions/x/1.json"); err == nil || !strings.Contains(err.Error(), "save the document") {
		t.Fatalf("an unsaved document: %v", err)
	}
	s.SetName("report")
	if err := s.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVersion(ctx); err != nil {
		t.Fatal(err)
	}
	old := s.Versions().Versions[0]
	text = "edited text that is not saved anywhere\n"
	if err := s.RestoreVersion(ctx, old.Key); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("the snapshot is opened once: %d", len(opened))
	}
	vs := s.Versions().Versions
	if len(vs) != 2 {
		t.Fatalf("the edited text is kept as a version: %d versions", len(vs))
	}
	kept := vs[0] // newest first
	if kept.Key == old.Key || !strings.HasPrefix(kept.Name, "Before restoring ") || kept.Author != "ana@example.com" {
		t.Errorf("the kept version: %+v", kept)
	}
	doc, err := s.Saver.LoadVersion(ctx, s.targetName(), kept.Key)
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, it := range doc.Content {
		if it.Chars != nil {
			got.WriteString(it.Chars.Text)
		}
	}
	if !strings.Contains(got.String(), "edited text") {
		t.Errorf("the kept version holds the edited text: %q", got.String())
	}
	if err := s.RestoreVersion(ctx, "versions/"+s.ID()+"/00000000000000000001.json"); err == nil {
		t.Error("a snapshot that is not there is an error")
	}
	if n := len(s.Versions().Versions); n != 2 {
		t.Errorf("a failed restore keeps nothing extra: %d versions", n)
	}
}
