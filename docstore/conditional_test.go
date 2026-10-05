//go:build !js

package docstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// plain hides every optional method of an adapter.
type plain struct{ Adapter }

func conditionalAdapters(t *testing.T) map[string]ConditionalAdapter {
	t.Helper()
	ctx := context.Background()
	sq, err := NewSQLiteAdapter(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sq.Close() })
	fs, err := NewLocalFSAdapter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer(NewMemoryAdapter(), "memory").Handler())
	t.Cleanup(ts.Close)
	return map[string]ConditionalAdapter{"memory": NewMemoryAdapter(), "sqlite": sq, "localfs": fs, "http": NewHTTPAdapter(ts.URL, nil)}
}

func TestSaveIfOnlySavesWhatWasRead(t *testing.T) {
	ctx := context.Background()
	for name, a := range conditionalAdapters(t) {
		// A new document: prev "" means it must not exist yet.
		if err := a.SaveIf(ctx, "x", []byte("one"), ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := a.SaveIf(ctx, "x", []byte("again"), ""); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: creating what exists: %v", name, err)
		}
		if err := a.SaveIf(ctx, "x", []byte("two"), Digest([]byte("one"))); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Somebody else changed it: the stale writer is told.
		if err := a.SaveIf(ctx, "x", []byte("three"), Digest([]byte("one"))); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: stale save: %v", name, err)
		}
		if err := a.SaveIf(ctx, "missing", []byte("v"), Digest([]byte("v"))); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: a document that is not there: %v", name, err)
		}
		if d, _ := a.Load(ctx, "x"); string(d.Data) != "two" {
			t.Errorf("%s: holds %q", name, d.Data)
		}
	}
}

func TestSaveIfLetsExactlyOneConcurrentWriterWin(t *testing.T) {
	ctx := context.Background()
	for name, a := range conditionalAdapters(t) {
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
					t.Errorf("%s: %v", name, err)
				}
			}()
		}
		wg.Wait()
		close(wins)
		if n := len(wins); n != 1 {
			t.Errorf("%s: %d writers won", name, n)
		}
	}
}

func TestServerOnAStoreWithoutSaveIfSaysSo(t *testing.T) {
	ts := httptest.NewServer(NewServer(plain{NewMemoryAdapter()}, "plain").Handler())
	defer ts.Close()
	if err := NewHTTPAdapter(ts.URL, nil).SaveIf(context.Background(), "x", []byte("v"), ""); !errors.Is(err, ErrConditionalUnsupported) {
		t.Fatal(err)
	}
}

func TestServerOnlyTakesTheConditionsItMeans(t *testing.T) {
	ts := httptest.NewServer(NewServer(NewMemoryAdapter(), "memory").Handler())
	defer ts.Close()
	put := func(hdr, val string) int {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/docs/x", strings.NewReader("v"))
		req.Header.Set(hdr, val)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if c := put("If-Match", "*"); c != http.StatusBadRequest {
		t.Errorf("If-Match: * is not a digest: %d", c)
	}
	if c := put("If-None-Match", `"abc"`); c != http.StatusBadRequest {
		t.Errorf("If-None-Match with a value other than *: %d", c)
	}
	if c := put("If-None-Match", "*"); c != http.StatusNoContent {
		t.Errorf("create: %d", c)
	}
	if c := put("If-None-Match", "*"); c != http.StatusPreconditionFailed {
		t.Errorf("create again: %d", c)
	}
	if c := put("If-Match", `"`+Digest([]byte("v"))+`"`); c != http.StatusNoContent {
		t.Errorf("replace what was read, quoted: %d", c)
	}
	if c := put("If-Match", Digest([]byte("zzz"))); c != http.StatusPreconditionFailed {
		t.Errorf("replace what was not: %d", c)
	}
}
