package fonts

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type memBlobs struct {
	mu   sync.Mutex
	m    map[string][]byte
	fail bool
}

func (b *memBlobs) Put(_ context.Context, k string, d []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("disk full")
	}
	if b.m == nil {
		b.m = map[string][]byte{}
	}
	b.m[k] = d
	return nil
}

func (b *memBlobs) Get(_ context.Context, k string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.m[k]
	return d, ok, nil
}

func lato() Font {
	return Font{Family: "Lato", Source: "Google", Variants: []Variant{
		{Weight: 400, URL: "https://f.test/lato-400.woff2"},
		{Weight: 700, URL: "https://f.test/lato-700.ttf"},
	}}
}

func TestLibraryDownloadsOnFirstUseOnly(t *testing.T) {
	blobs := &memBlobs{}
	var fetched []string
	var ready []Ready
	lib := &Library{Blobs: blobs,
		Fetch: func(_ context.Context, u string) ([]byte, error) {
			fetched = append(fetched, u)
			return []byte("bytes:" + u), nil
		},
		OnReady: func(r Ready) { ready = append(ready, r) }}
	lib.Add(lato())
	if len(fetched) != 0 || len(blobs.m) != 0 {
		t.Fatal("Add downloads nothing")
	}
	ctx := context.Background()
	key, err := lib.Use(ctx, "Lato", 700, false)
	if err != nil || key != "fonts/Lato/700-n.ttf" {
		t.Fatalf("%q %v", key, err)
	}
	if len(fetched) != 1 || string(blobs.m[key]) != "bytes:https://f.test/lato-700.ttf" {
		t.Fatalf("fetched %v store %v", fetched, blobs.m)
	}
	if len(ready) != 1 || !ready[0].Downloaded || ready[0].Variant.Key != key {
		t.Fatalf("%+v", ready)
	}
	// Again: nothing more happens.
	if _, err := lib.Use(ctx, "Lato", 700, false); err != nil || len(fetched) != 1 || len(ready) != 1 {
		t.Fatalf("second use: %v %v %v", err, fetched, ready)
	}
	// 600 is nearest the 700 cut too.
	if k, _ := lib.Use(ctx, "Lato", 600, false); k != key || len(fetched) != 1 {
		t.Fatal(k)
	}
	// The recorded font carries the key.
	for _, v := range lib.Added()[0].Variants {
		if (v.Weight == 700) != (v.Key == key) {
			t.Fatalf("variant keys: %+v", lib.Added()[0].Variants)
		}
	}
}

func TestLibraryLoadsFromStoreWithoutFetching(t *testing.T) {
	blobs := &memBlobs{m: map[string][]byte{"fonts/Lato/400-n.woff2": []byte("kept")}}
	var ready []Ready
	lib := &Library{Blobs: blobs,
		Fetch:   func(context.Context, string) ([]byte, error) { t.Fatal("must not fetch"); return nil, nil },
		OnReady: func(r Ready) { ready = append(ready, r) }}
	lib.Add(lato())
	if _, err := lib.Use(context.Background(), "Lato", 400, false); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Downloaded || string(ready[0].Data) != "kept" {
		t.Fatalf("%+v", ready)
	}
}

func TestLibraryNotAddedAndInstalledFonts(t *testing.T) {
	lib := &Library{Blobs: &memBlobs{}}
	if _, err := lib.Use(context.Background(), "Nope", 400, false); !errors.Is(err, ErrNotAdded) {
		t.Fatal(err)
	}
	lib.Add(Font{Family: "Local", Variants: []Variant{{Weight: 400}}}) // no URL: installed
	if k, err := lib.Use(context.Background(), "Local", 400, false); k != "" || err != nil {
		t.Fatalf("%q %v", k, err)
	}
	lib.Add(Font{Family: "Empty"})
	if k, err := lib.Use(context.Background(), "Empty", 400, false); k != "" || err != nil {
		t.Fatalf("%q %v", k, err)
	}
}

func TestLibraryErrorsAreReportedAndRetried(t *testing.T) {
	blobs := &memBlobs{fail: true}
	var errs []string
	calls := 0
	lib := &Library{Blobs: blobs, Fetch: func(context.Context, string) ([]byte, error) { calls++; return []byte("x"), nil },
		OnError: func(f string, _ error) { errs = append(errs, f) }}
	lib.Add(lato())
	if _, err := lib.Use(context.Background(), "Lato", 400, false); err == nil || len(errs) != 1 {
		t.Fatalf("store failure: %v %v", err, errs)
	}
	blobs.fail = false
	if _, err := lib.Use(context.Background(), "Lato", 400, false); err != nil || calls != 2 {
		t.Fatalf("retry: %v %d", err, calls)
	}
	lib.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("404") }
	lib.Add(Font{Family: "Other", Variants: []Variant{{Weight: 400, URL: "u"}}})
	if _, err := lib.Use(context.Background(), "Other", 400, false); err == nil || len(errs) != 2 {
		t.Fatalf("fetch failure: %v %v", err, errs)
	}
	noStore := &Library{OnError: func(f string, _ error) { errs = append(errs, "nostore") }}
	noStore.Add(lato())
	if _, err := noStore.Use(context.Background(), "Lato", 400, false); err == nil || errs[len(errs)-1] != "nostore" {
		t.Fatal(err)
	}
}

func TestLibraryAddKeepsKeysAndUseAsync(t *testing.T) {
	blobs := &memBlobs{}
	done := make(chan Ready, 1)
	lib := &Library{Blobs: blobs, Fetch: func(context.Context, string) ([]byte, error) { return []byte("x"), nil },
		OnReady: func(r Ready) { done <- r }}
	lib.Add(lato())
	lib.UseAsync("Lato", 400, false)
	lib.UseAsync("Unknown", 400, false) // ignored
	r := <-done
	lib.Add(lato()) // re-added from the selector: keys survive
	for _, v := range lib.Added()[0].Variants {
		if v.Weight == 400 && v.Key != r.Variant.Key {
			t.Fatalf("key lost: %+v", lib.Added()[0].Variants)
		}
	}
}

func TestKey(t *testing.T) {
	if k := Key("A/B", Variant{Weight: 0, Italic: true, URL: "https://x/y.WOFF"}); k != "fonts/A_B/400-i.woff" {
		t.Fatal(k)
	}
	if k := Key("A", Variant{Weight: 300, URL: "https://x/y"}); k != "fonts/A/300-n.font" {
		t.Fatal(k)
	}
}

func TestLoadFaceNeedsBrowser(t *testing.T) {
	var got error
	LoadFace("A", Variant{}, nil, func(err error) { got = err })
	if got == nil {
		t.Fatal("no browser")
	}
	LoadFace("A", Variant{}, nil, nil)
}
