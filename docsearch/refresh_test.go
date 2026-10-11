package docsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

type countingStore struct {
	*docstore.MemoryAdapter
	loads   int
	failFor string
}

func (c *countingStore) Load(ctx context.Context, id string) (docstore.Document, error) {
	c.loads++
	if id == c.failFor {
		return docstore.Document{}, errors.New("boom")
	}
	return c.MemoryAdapter.Load(ctx, id)
}

func put(t *testing.T, s docstore.Adapter, id, title, text string) {
	t.Helper()
	b, err := docfile.JSON.Marshal(docfile.Document{Title: title, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), id, b); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshReadsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	st := &countingStore{MemoryAdapter: docstore.NewMemoryAdapter()}
	put(t, st, "a", "Budget", "yearly numbers")
	put(t, st, "b", "Notes", "meeting on tuesday")
	var ix Index
	r, err := Refresh(ctx, &ix, st, FromDocfile)
	if err != nil || r.Added != 2 || st.loads != 2 {
		t.Fatalf("first: %+v %v loads=%d", r, err, st.loads)
	}
	if got := ids(ix.Search("tuesday", 0)); got != "b" {
		t.Fatalf("%q", got)
	}

	st.loads = 0
	if r, _ = Refresh(ctx, &ix, st, FromDocfile); r.Unchanged != 2 || st.loads != 0 {
		t.Fatalf("nothing changed, nothing read: %+v loads=%d", r, st.loads)
	}

	put(t, st, "b", "Notes", "meeting on friday")
	put(t, st, "c", "Recipes", "flour")
	if err := st.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	st.loads = 0
	r, _ = Refresh(ctx, &ix, st, FromDocfile)
	if r.Added != 1 || r.Updated != 1 || r.Removed != 1 || r.Unchanged != 0 || st.loads != 2 {
		t.Fatalf("%+v loads=%d", r, st.loads)
	}
	if ids(ix.Search("tuesday", 0)) != "" || ids(ix.Search("friday", 0)) != "b" || ids(ix.Search("yearly", 0)) != "" {
		t.Fatal("old text and deleted documents are gone")
	}
}

func TestRefreshSkipsWhatItCannotReadAndSurvivesSealing(t *testing.T) {
	ctx := context.Background()
	st := &countingStore{MemoryAdapter: docstore.NewMemoryAdapter()}
	put(t, st, "a", "Good", "readable words")
	if err := st.Save(ctx, "junk", []byte("not a document \x00\x01")); err != nil {
		t.Fatal(err)
	}
	put(t, st, "broken", "Broken", "x")
	st.failFor = "broken"
	var ix Index
	r, err := Refresh(ctx, &ix, st, FromDocfile)
	if err != nil || r.Added != 1 || len(r.Skipped) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	key, _ := DeriveKey([]byte("secret"), "store")
	sealed, err := ix.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	st.loads = 0
	if r, _ = Refresh(ctx, back, st, FromDocfile); r.Unchanged != 1 {
		t.Fatalf("a reopened index remembers what it holds: %+v", r)
	}
}

func TestRefreshStopsWhenCancelledAndReportsListErrors(t *testing.T) {
	st := &countingStore{MemoryAdapter: docstore.NewMemoryAdapter()}
	put(t, st, "a", "A", "one")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ix Index
	if _, err := Refresh(ctx, &ix, st, FromDocfile); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := Refresh(context.Background(), &ix, failList{}, FromDocfile); err == nil {
		t.Fatal("a failing list is an error")
	}
}

type failList struct{ docstore.Adapter }

func (failList) List(context.Context) ([]docstore.Meta, error) { return nil, errors.New("down") }

func TestFromDocfileReadsTablesAndEveryFormat(t *testing.T) {
	d := docfile.Document{Title: "T", Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "para"}},
		{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"c1", "c2"}}}}},
	}}
	for _, c := range docfile.Formats() {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		title, text, err := FromDocfile(b)
		if err != nil || title != "T" || text == "" {
			t.Fatalf("%s: %q %q %v", c.Name(), title, text, err)
		}
		var ix Index
		ix.Add("x", title, text)
		if len(ix.Search("c2", 0)) != 1 {
			t.Fatalf("%s: table cells are searched (%q)", c.Name(), text)
		}
	}
}
