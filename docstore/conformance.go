package docstore

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
)

// CheckAdapter runs the same behavioral contract against an adapter.
func CheckAdapter(t *testing.T, newAdapter func() Adapter) {
	t.Helper()
	ctx := context.Background()

	t.Run("load missing returns ErrNotFound", func(t *testing.T) {
		a := newAdapter()
		if _, err := a.Load(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Load(missing) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete missing returns ErrNotFound", func(t *testing.T) {
		a := newAdapter()
		if err := a.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(missing) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("save then load round-trips the payload", func(t *testing.T) {
		a := newAdapter()
		want := []byte(`{"blocks":[]}`)
		if err := a.Save(ctx, "doc1", want); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := a.Load(ctx, "doc1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if string(got.Data) != string(want) {
			t.Fatalf("Load data = %q, want %q", got.Data, want)
		}
		if got.ID != "doc1" {
			t.Fatalf("Load ID = %q, want doc1", got.ID)
		}
		if got.UpdatedAt.IsZero() {
			t.Fatalf("Load UpdatedAt is zero, want a real timestamp")
		}
	})

	t.Run("save overwrites an existing document", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "doc1", []byte("v1")); err != nil {
			t.Fatalf("Save v1: %v", err)
		}
		if err := a.Save(ctx, "doc1", []byte("v2")); err != nil {
			t.Fatalf("Save v2: %v", err)
		}
		got, err := a.Load(ctx, "doc1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if string(got.Data) != "v2" {
			t.Fatalf("Load data = %q, want v2", got.Data)
		}
	})

	t.Run("list reflects saved and deleted documents", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "a", []byte("aaa")); err != nil {
			t.Fatalf("Save a: %v", err)
		}
		if err := a.Save(ctx, "b", []byte("bb")); err != nil {
			t.Fatalf("Save b: %v", err)
		}
		metas, err := a.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(metas) != 2 {
			t.Fatalf("List returned %d entries, want 2", len(metas))
		}
		sizes := map[string]int{}
		for _, m := range metas {
			sizes[m.ID] = m.Size
		}
		if sizes["a"] != 3 || sizes["b"] != 2 {
			t.Fatalf("List sizes = %v, want a=3 b=2", sizes)
		}

		if err := a.Delete(ctx, "a"); err != nil {
			t.Fatalf("Delete a: %v", err)
		}
		metas, err = a.List(ctx)
		if err != nil {
			t.Fatalf("List after delete: %v", err)
		}
		if len(metas) != 1 || metas[0].ID != "b" {
			t.Fatalf("List after delete = %v, want just [b]", metas)
		}
	})

	t.Run("deleted document is truly gone", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "doc1", []byte("x")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := a.Delete(ctx, "doc1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := a.Load(ctx, "doc1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Load after delete err = %v, want ErrNotFound", err)
		}
	})

	t.Run("empty store lists as empty, not nil-panicking", func(t *testing.T) {
		a := newAdapter()
		metas, err := a.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(metas) != 0 {
			t.Fatalf("List on empty store = %v, want empty", metas)
		}
	})
}

// CheckConditional runs the contract of ConditionalAdapter.
func CheckConditional(t *testing.T, newAdapter func() ConditionalAdapter) {
	t.Helper()
	ctx := context.Background()
	t.Run("only what was read is saved", func(t *testing.T) {
		a := newAdapter()
		if err := a.SaveIf(ctx, "x", []byte("one"), ""); err != nil {
			t.Fatal(err)
		}
		if err := a.SaveIf(ctx, "x", []byte("again"), ""); !errors.Is(err, ErrConflict) {
			t.Errorf("creating what exists: %v", err)
		}
		if err := a.SaveIf(ctx, "x", []byte("two"), Digest([]byte("one"))); err != nil {
			t.Fatal(err)
		}
		if err := a.SaveIf(ctx, "x", []byte("three"), Digest([]byte("one"))); !errors.Is(err, ErrConflict) {
			t.Errorf("a stale save: %v", err)
		}
		if err := a.SaveIf(ctx, "missing", []byte("v"), Digest([]byte("v"))); !errors.Is(err, ErrConflict) {
			t.Errorf("a document that is not there: %v", err)
		}
		if d, err := a.Load(ctx, "x"); err != nil || string(d.Data) != "two" {
			t.Errorf("holds %q: %v", d.Data, err)
		}
	})
	t.Run("one of several writers wins", func(t *testing.T) {
		a := newAdapter()
		_ = a.SaveIf(ctx, "c", []byte("0"), "")
		var wg sync.WaitGroup
		wins := make(chan int, 8)
		for i := 1; i <= 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := a.SaveIf(ctx, "c", []byte{byte('0' + i)}, Digest([]byte("0"))); err == nil {
					wins <- i
				} else if !errors.Is(err, ErrConflict) {
					t.Errorf("%v", err)
				}
			}()
		}
		wg.Wait()
		close(wins)
		n := 0
		for range wins {
			n++
		}
		if n != 1 {
			t.Errorf("%d writers won, want 1", n)
		}
	})
}

// CheckPrefixLister runs the contract of PrefixLister.
func CheckPrefixLister(t *testing.T, newAdapter func() Adapter) {
	t.Helper()
	ctx := context.Background()
	a := newAdapter()
	pl, ok := a.(PrefixLister)
	if !ok {
		t.Fatalf("%T is not a PrefixLister", a)
	}
	ids := []string{"directory/a/1", "directory/a/22", "directory/ab/1", "directory/b/1", "other", "directory",
		"pct%/1", "pctX/1", "under_/1", "underX/1", "dot.x/1", "dotYx/1", "star*/1", "stars/1"}
	for _, id := range ids {
		if err := a.Save(ctx, id, []byte(id)); err != nil {
			t.Fatalf("Save(%q): %v", id, err)
		}
	}
	want := map[string][]string{
		"directory/a/": {"directory/a/1", "directory/a/22"},
		"pct%/":        {"pct%/1"},
		"under_/":      {"under_/1"},
		"dot.x/":       {"dot.x/1"},
		"star*/":       {"star*/1"},
		"directory/":   {"directory/a/1", "directory/a/22", "directory/ab/1", "directory/b/1"},
		"nothing/":     nil,
	}
	for prefix, exp := range want {
		got, err := pl.ListPrefix(ctx, prefix)
		if err != nil {
			t.Fatalf("ListPrefix(%q): %v", prefix, err)
		}
		var have []string
		for _, m := range got {
			have = append(have, m.ID)
			if m.Size != len(m.ID) {
				t.Errorf("ListPrefix(%q): size of %s = %d", prefix, m.ID, m.Size)
			}
		}
		sort.Strings(have)
		sort.Strings(exp)
		if strings.Join(have, ",") != strings.Join(exp, ",") {
			t.Errorf("ListPrefix(%q) = %v, want %v", prefix, have, exp)
		}
	}
}

// CheckHeadReader runs the contract of HeadReader.
func CheckHeadReader(t *testing.T, newAdapter func() Adapter) {
	t.Helper()
	ctx := context.Background()
	a := newAdapter()
	hr, ok := a.(HeadReader)
	if !ok {
		t.Fatalf("%T is not a HeadReader", a)
	}
	big := make([]byte, 5000)
	for i := range big {
		big[i] = byte(i * 7) // every byte value, zeros and high bytes included
	}
	for id, data := range map[string][]byte{"big": big, "small": []byte("abc"), "empty": {}} {
		if err := a.Save(ctx, id, data); err != nil {
			t.Fatalf("Save(%q): %v", id, err)
		}
	}
	if _, err := hr.LoadHead(ctx, "missing", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("LoadHead(missing) err = %v, want ErrNotFound", err)
	}
	for _, c := range []struct {
		id   string
		n    int
		want []byte
	}{
		{"big", 1, big[:1]}, {"big", 100, big[:100]}, {"big", 4999, big[:4999]}, {"big", 5000, big},
		{"big", 5001, big}, {"big", 1 << 20, big},
		{"small", 2, []byte("ab")}, {"small", 3, []byte("abc")}, {"small", 10, []byte("abc")},
		{"empty", 10, nil},
	} {
		got, err := hr.LoadHead(ctx, c.id, c.n)
		if err != nil {
			t.Errorf("LoadHead(%q, %d): %v", c.id, c.n, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("LoadHead(%q, %d) = %d bytes, want %d", c.id, c.n, len(got), len(c.want))
		}
		head, whole, err := LoadHead(ctx, a, c.id, c.n)
		if err != nil || !bytes.Equal(head, c.want) || whole != (len(c.want) < c.n) {
			t.Errorf("helper LoadHead(%q, %d) = %d bytes, whole %v, %v", c.id, c.n, len(head), whole, err)
		}
	}
	head, whole, err := LoadHead(ctx, struct{ Adapter }{a}, "big", 10)
	if err != nil || len(head) != len(big) || !whole {
		t.Errorf("helper without HeadReader = %d bytes, whole %v, %v", len(head), whole, err)
	}
}
