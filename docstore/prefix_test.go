package docstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func doRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// plainAdapter hides the optional interfaces of the adapter it wraps.
type plainAdapter struct{ Adapter }

func TestListPrefixOnEveryAdapter(t *testing.T) {
	ctx := context.Background()
	lf, err := NewLocalFSAdapter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adapters := map[string]Adapter{"memory": NewMemoryAdapter(), "localfs": lf, "fallback": plainAdapter{NewMemoryAdapter()}}
	if sq := sqliteForTest(t); sq != nil {
		adapters["sqlite"] = sq
	}
	for name, a := range adapters {
		for _, id := range []string{"directory/a/1", "directory/a/2", "directory/ab/1", "directory/b/1", "other", "directory"} {
			if err := a.Save(ctx, id, []byte(id)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		got, err := ListPrefix(ctx, a, "directory/a/")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var ids []string
		for _, m := range got {
			ids = append(ids, m.ID)
			if m.Size != len(m.ID) {
				t.Errorf("%s: size of %s = %d", name, m.ID, m.Size)
			}
		}
		sort.Strings(ids)
		if len(ids) != 2 || ids[0] != "directory/a/1" || ids[1] != "directory/a/2" {
			t.Errorf("%s: %v", name, ids)
		}
		if _, ok := a.(PrefixLister); ok != (name != "fallback") {
			t.Errorf("%s: PrefixLister = %v", name, ok)
		}
		if none, err := ListPrefix(ctx, a, "nothing/"); err != nil || len(none) != 0 {
			t.Errorf("%s: %v %v", name, none, err)
		}
	}
}

func TestDirectoryKeyspaceIsReservedFromTheDocumentAPI(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAdapter()
	if err := store.Save(ctx, "directory/x/y", []byte("record")); err != nil {
		t.Fatal(err)
	}
	h := NewServer(store, "memory").Handler()
	for _, c := range []struct{ method, path string }{
		{"PUT", "/docs/directory/x/y"}, {"GET", "/docs/directory/x/y"}, {"DELETE", "/docs/directory/x/y"},
		{"PUT", "/docs/directory/new"}, {"PUT", "/docs/directory/x/y/acl"}, {"GET", "/docs/directory/x/y/acl"},
	} {
		rec := doRequest(h, c.method, c.path, "forged")
		if rec.Code != 400 {
			t.Errorf("%s %s = %d, want 400", c.method, c.path, rec.Code)
		}
	}
	if d, _ := store.Load(ctx, "directory/x/y"); string(d.Data) != "record" {
		t.Errorf("the record changed: %q", d.Data)
	}
	if _, err := store.Load(ctx, "directory/new"); err == nil {
		t.Error("a record was created")
	}
	rec := doRequest(h, "GET", "/docs", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if s := rec.Body.String(); strings.Contains(s, "directory") {
		t.Errorf("listed: %s", s)
	}
}
