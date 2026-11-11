package docsearch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func text(t *testing.T, s docstore.Adapter, id string) string {
	t.Helper()
	d, err := s.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := FromDocfile(d.Data)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPlanThenApplyThenUndo(t *testing.T) {
	ctx := context.Background()
	st := docstore.NewMemoryAdapter()
	put(t, st, "a", "Budget", "Acme pays Acme. Acme again.")
	put(t, st, "b", "Notes", "Nothing about it here")
	put(t, st, "c", "Plan", "Ask acme about the plan")
	if err := st.Save(ctx, "junk", []byte("\x00\x01 not a document")); err != nil {
		t.Fatal(err)
	}
	p, err := PlanReplace(ctx, st, canvas.FindOptions{Query: "acme"}, "Globex")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 2 || p.Total() != 4 || len(p.Skipped) != 1 || p.Skipped[0] != "junk" {
		t.Fatalf("%+v", p)
	}
	var a Change
	for _, c := range p.Changes {
		if c.ID == "a" {
			a = c
		}
	}
	if a.Count != 3 || !strings.Contains(a.Before, "Acme") || !strings.Contains(a.After, "Globex") || a.Title != "Budget" {
		t.Fatalf("the preview shows the first match as it is and as it would be: %+v", a)
	}
	if text(t, st, "a") != "Acme pays Acme. Acme again." {
		t.Fatal("planning writes nothing")
	}

	done, err := p.Apply(ctx)
	if err != nil || len(done.Done) != 2 || len(done.Conflicts) != 0 {
		t.Fatalf("%+v %v", done, err)
	}
	if text(t, st, "a") != "Globex pays Globex. Globex again." || text(t, st, "c") != "Ask Globex about the plan" || text(t, st, "b") != "Nothing about it here" {
		t.Fatalf("applied: %q %q", text(t, st, "a"), text(t, st, "c"))
	}
	d, _ := st.Load(ctx, "a")
	if doc, _, _ := decode(d.Data); doc.Title != "Budget" {
		t.Fatalf("title %q", doc.Title)
	}

	restored, kept, err := done.Undo(ctx)
	if err != nil || len(restored) != 2 || len(kept) != 0 {
		t.Fatalf("%v %v %v", restored, kept, err)
	}
	if text(t, st, "a") != "Acme pays Acme. Acme again." || text(t, st, "c") != "Ask acme about the plan" {
		t.Fatal("undone")
	}
}

func TestApplyOnlyTheTickedAndNeverOverEdits(t *testing.T) {
	ctx := context.Background()
	st := docstore.NewMemoryAdapter()
	put(t, st, "a", "A", "foo one")
	put(t, st, "b", "B", "foo two")
	p, _ := PlanReplace(ctx, st, canvas.FindOptions{Query: "foo"}, "bar")
	put(t, st, "b", "B", "foo two, edited meanwhile") // somebody saved after the plan
	done, err := p.Apply(ctx)
	if err != nil || len(done.Done) != 1 || done.Done[0] != "a" || len(done.Conflicts) != 1 || done.Conflicts[0] != "b" {
		t.Fatalf("%+v %v", done, err)
	}
	if text(t, st, "b") != "foo two, edited meanwhile" {
		t.Fatal("the newer edit is kept")
	}

	p2, _ := PlanReplace(ctx, st, canvas.FindOptions{Query: "foo"}, "baz")
	only, _ := p2.Apply(ctx, "b")
	if len(only.Done) != 1 || text(t, st, "b") != "baz two, edited meanwhile" {
		t.Fatalf("only b: %+v", only)
	}
	put(t, st, "b", "B", "saved again after the replace")
	restored, kept, _ := only.Undo(ctx)
	if len(restored) != 0 || len(kept) != 1 || text(t, st, "b") != "saved again after the replace" {
		t.Fatalf("undo must not discard a later save: %v %v", restored, kept)
	}
}

func TestPlanRegexOptionsAndErrors(t *testing.T) {
	ctx := context.Background()
	st := docstore.NewMemoryAdapter()
	put(t, st, "a", "A", "order 12 and order 345")
	p, err := PlanReplace(ctx, st, canvas.FindOptions{Query: `order (\d+)`, Regex: true}, "no. $1")
	if err != nil || p.Total() != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := p.Apply(ctx); err != nil || text(t, st, "a") != "no. 12 and no. 345" {
		t.Fatalf("%q %v", text(t, st, "a"), err)
	}
	if _, err := PlanReplace(ctx, st, canvas.FindOptions{Query: ""}, "x"); !errors.Is(err, canvas.ErrEmptyQuery) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := PlanReplace(ctx, st, canvas.FindOptions{Query: "(", Regex: true}, "x"); err == nil {
		t.Fatal("a bad pattern is an error before anything is read")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PlanReplace(cctx, st, canvas.FindOptions{Query: "no"}, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := PlanReplace(ctx, failList{}, canvas.FindOptions{Query: "no"}, "x"); err == nil {
		t.Fatal("list error")
	}
	_ = docfile.JSON
}

func TestPlanBytesGivesTheOldAndTheReplacement(t *testing.T) {
	ctx := context.Background()
	st := docstore.NewMemoryAdapter()
	put(t, st, "a", "Budget", "Acme pays")
	put(t, st, "b", "Notes", "Nothing here")
	p, err := PlanReplace(ctx, st, canvas.FindOptions{Query: "acme"}, "Globex")
	if err != nil {
		t.Fatal(err)
	}
	old, repl, ok := p.Bytes("a")
	if !ok {
		t.Fatal("no bytes for a changed document")
	}
	if _, body, _ := FromDocfile(old); !strings.Contains(body, "Acme") {
		t.Errorf("the old bytes hold %q", body)
	}
	if _, body, _ := FromDocfile(repl); !strings.Contains(body, "Globex") || strings.Contains(body, "Acme") {
		t.Errorf("the replacement holds %q", body)
	}
	if _, _, ok := p.Bytes("b"); ok {
		t.Error("bytes for a document without a match")
	}
	old[0] ^= 0xff
	again, _, _ := p.Bytes("a")
	if again[0] == old[0] {
		t.Error("Bytes returned the plan's own slice")
	}
}
