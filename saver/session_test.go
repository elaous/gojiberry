package saver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
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
	s.Go = func(f func()) { f() }
	return s, &opened, &changes
}

func TestSessionSaveOpenDeleteFlow(t *testing.T) {
	ctx := context.Background()
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
	if p.Failed || p.Status != "Saved report.pb" || len(p.Entries) != 1 || p.Entries[0].Name != "report" || p.Entries[0].Format != "Protobuf (binary)" {
		t.Fatalf("after save: %+v", p)
	}
	if *changes == 0 {
		t.Fatal("OnChange never called")
	}
	// Another target starts empty; coming back finds it.
	s.Props().OnTarget("memory")
	if p = s.Props(); len(p.Entries) != 0 || p.Target != "memory" {
		t.Fatalf("%+v", p)
	}
	s.Props().OnTarget("sqlite")
	if p = s.Props(); len(p.Entries) != 1 {
		t.Fatalf("%+v", p)
	}
	// Opening hands over the document and adopts its name and format.
	s.SetName("other")
	s.SetFormat("json")
	s.Props().OnOpen("report.pb")
	if len(*opened) != 1 || (*opened)[0].Title != "report" || s.Name() != "report" || s.Props().Format != "proto" || s.Props().Status != "Opened report.pb" {
		t.Fatalf("%+v %q %+v", *opened, s.Name(), s.Props())
	}
	s.Props().OnRefresh()
	s.Props().OnDelete("report.pb")
	if p = s.Props(); len(p.Entries) != 0 || p.Status != "Deleted report.pb" {
		t.Fatalf("%+v", p)
	}
	_ = ctx
	if !strings.Contains(s.String(), "sqlite") {
		t.Fatal(s.String())
	}
}

func TestSessionErrors(t *testing.T) {
	s, opened, _ := newSession(t)
	s.Save(context.Background()) // no name
	if p := s.Props(); !p.Failed || p.Busy || !strings.Contains(p.Status, "name") {
		t.Fatalf("%+v", p)
	}
	s.SetName("x")
	s.SetFormat("nope")
	s.Save(context.Background())
	if p := s.Props(); !p.Failed || !strings.Contains(p.Status, "no format") {
		t.Fatalf("%+v", p)
	}
	s.Open(context.Background(), "missing.json")
	if p := s.Props(); !p.Failed || len(*opened) != 0 {
		t.Fatalf("%+v", p)
	}
	s.Delete(context.Background(), "missing.json")
	if !s.Props().Failed {
		t.Fatal("delete of a missing document fails")
	}
	s.SetTarget(context.Background(), "nope")
	if !s.Props().Failed {
		t.Fatal("listing an unknown target fails")
	}
	// Opening something with no known extension keeps the name and format.
	s.SetTarget(context.Background(), "memory")
	_, _ = s.Saver.Targets[2].Adapter, 0
	if err := s.Saver.Targets[2].Adapter.Save(context.Background(), "plain", []byte(`{"title":"P"}`)); err != nil {
		t.Fatal(err)
	}
	s.SetName("keep")
	s.SetFormat("json")
	s.Open(context.Background(), "plain")
	if s.Name() != "keep" || len(*opened) != 1 {
		t.Fatal(s.Name())
	}
	// Defaults: no OnChange, no OnOpen, a real goroutine.
	bare := NewSession(&Saver{}, func() docfile.Document { return docfile.Document{} }, nil)
	bare.Open(context.Background(), "x")
	bare.SetName("n")
	done := make(chan struct{})
	bare.Go = func(f func()) { f(); close(done) }
	bare.Props().OnRefresh()
	<-done
	if bare.Props().Target != "" {
		t.Fatal("no targets, no target")
	}
}

func TestSessionRealGoroutine(t *testing.T) {
	s, _, _ := newSession(t)
	s.Go = nil
	got := make(chan struct{}, 8)
	s.OnChange = func() { got <- struct{}{} }
	s.Props().OnRefresh()
	<-got
}

func TestSessionTouchesTheEditorThroughOnUI(t *testing.T) {
	ctx := context.Background()
	s, opened, _ := newSession(t)
	calls := 0
	s.OnUI = func(f func()) { calls++; f() }
	s.SetName("doc")
	s.Save(ctx)
	if calls != 1 {
		t.Fatalf("Save read the editor %d times through OnUI, want 1", calls)
	}
	s.Open(ctx, "doc.json")
	if calls != 2 || len(*opened) != 1 {
		t.Fatalf("Open delivered the document through OnUI: calls=%d opened=%d", calls, len(*opened))
	}
}

func TestSessionLifecycleThroughTheComponentsHandlers(t *testing.T) {
	ctx := context.Background()
	s, opened, _ := newSession(t)
	s.SetName("report")
	s.Props().OnSave()
	s.Props().OnSaveVersion()
	p := s.Props()
	if len(p.Versions) != 1 || p.Status != "Saved a version of report" {
		t.Fatalf("after a version: %+v", p)
	}
	s.Props().OnOpenVersion(p.Versions[0].Key)
	if len(*opened) != 1 || s.Props().Status != "Opened a version" {
		t.Fatalf("opening a version: %d opened, %q", len(*opened), s.Props().Status)
	}
	s.Props().OnDeleteVersion(s.Props().Versions[0].Key)
	if len(s.Props().Versions) != 0 {
		t.Error("version not deleted")
	}

	// Tags, and filtering by them.
	s.SetName("second")
	s.Props().OnSave()
	s.Props().OnTags("report.json", []string{"rpg", "draft"})
	s.Props().OnTags("second.json", []string{"draft"})
	p = s.Props()
	if !reflect.DeepEqual(p.AllTags, []string{"draft", "rpg"}) {
		t.Fatalf("all tags %v", p.AllTags)
	}
	s.Props().OnTagFilter("rpg")
	if p = s.Props(); p.TagFilter != "rpg" {
		t.Errorf("filter %q", p.TagFilter)
	}
	s.Props().OnTags("report.json", nil) // the last "rpg" goes: the filter must not strand the list
	if p = s.Props(); p.TagFilter != "" || len(p.AllTags) != 1 {
		t.Errorf("after the last rpg tag went: %q %v", p.TagFilter, p.AllTags)
	}

	// Duplicate, trash, restore, purge.
	s.Props().OnDuplicate("report.json")
	if p = s.Props(); len(p.Entries) != 3 || p.Status != "Copied report.json to Copy of report.json" {
		t.Fatalf("duplicate: %d entries, %q", len(p.Entries), p.Status)
	}
	s.Props().OnTrash("report.json")
	p = s.Props()
	if len(p.Entries) != 2 || len(p.Trashed) != 1 || p.Trashed[0].Name != "report" {
		t.Fatalf("trash: %+v", p)
	}
	s.Props().OnRestore(p.Trashed[0].Key)
	if p = s.Props(); len(p.Entries) != 3 || len(p.Trashed) != 0 || p.Status != "Restored report.json" {
		t.Fatalf("restore: %d entries, %d trashed, %q", len(p.Entries), len(p.Trashed), p.Status)
	}
	s.Props().OnTrash("second.json")
	s.Props().OnPurge(s.Props().Trashed[0].Key)
	if p = s.Props(); len(p.Trashed) != 0 || len(p.Entries) != 2 {
		t.Fatalf("purge: %+v", p)
	}
	s.Props().OnTrash("report.json")
	s.Props().OnTrash("Copy of report.json")
	s.Props().OnEmptyTrash()
	if p = s.Props(); len(p.Trashed) != 0 || p.Status != "Emptied the trash (2)" || p.Failed {
		t.Fatalf("empty: %+v", p)
	}
	s.Props().OnTrash("missing.json")
	if p = s.Props(); !p.Failed {
		t.Error("a failure must show")
	}
	_ = ctx
}
