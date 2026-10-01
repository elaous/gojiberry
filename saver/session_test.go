package saver

import (
	"context"
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
