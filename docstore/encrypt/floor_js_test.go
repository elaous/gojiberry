//go:build js && wasm

package encrypt

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
)

// Run under node, as auth/passkey/client's wasm test is:
//
//	GOOS=js GOARCH=wasm go test -exec "$(go env GOROOT)/lib/wasm/go_js_wasm_exec" -run LocalStorage ./docstore/encrypt
//
// (node has no localStorage here; the tests install a fake one that behaves like the
// browser's, including the ways it fails.)

// fakeLocalStorage installs a localStorage global backed by a JS object. throwOn names the
// method that throws ("" none); "accessor" makes reading the global itself throw, as a private
// window or blocked site data does.
func fakeLocalStorage(t *testing.T, throwOn string) {
	t.Helper()
	g := js.Global()
	src := `
	  var data = {};
	  var throwOn = ` + "'" + throwOn + "'" + `;
	  var ls = {
	    getItem: function(k) { if (throwOn === 'getItem') throw new Error('SecurityError'); return Object.prototype.hasOwnProperty.call(data, k) ? data[k] : null; },
	    setItem: function(k, v) { if (throwOn === 'setItem') throw new Error('QuotaExceededError'); data[k] = String(v); },
	    removeItem: function(k) { delete data[k]; }
	  };
	  return { ls: ls, data: data, throwOn: throwOn };`
	made := g.Get("Function").New(src).Invoke()
	desc := g.Get("Object").New()
	if throwOn == "accessor" {
		desc.Set("get", g.Get("Function").New("throw new Error('SecurityError: access denied');"))
	} else {
		desc.Set("value", made.Get("ls"))
	}
	desc.Set("configurable", true)
	g.Get("Object").Call("defineProperty", g, "localStorage", desc)
}

func TestLocalStorageSeqFloorPersistsInTheBrowsersStorage(t *testing.T) {
	ctx := context.Background()
	fakeLocalStorage(t, "")
	f := LocalStorageSeqFloor("gojiberry-floor:ana:")
	for _, n := range []uint64{4, 2, 6} {
		if err := f.Raise(ctx, "doc", n); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok := js.Global().Get("localStorage").Call("getItem", "gojiberry-floor:ana:doc"), true; !ok || v.String() != "6" {
		t.Fatalf("not in localStorage: %v", v)
	}
	// A reloaded page: a new floor over the same storage sees it, and only raises it.
	g := LocalStorageSeqFloor("gojiberry-floor:ana:")
	if v, err := g.Get(ctx, "doc"); err != nil || v != 6 {
		t.Fatalf("%d %v", v, err)
	}
	_ = g.Raise(ctx, "doc", 3)
	if v, _ := g.Get(ctx, "doc"); v != 6 {
		t.Errorf("%d", v)
	}
	// Another account's prefix is another floor.
	if v, _ := LocalStorageSeqFloor("gojiberry-floor:bo:").Get(ctx, "doc"); v != 0 {
		t.Errorf("%d", v)
	}
	if g.Err() != nil {
		t.Errorf("%v", g.Err())
	}
}

func TestLocalStorageSeqFloorFallsBackToMemoryWhenStorageThrows(t *testing.T) {
	ctx := context.Background()
	for _, throwOn := range []string{"accessor", "getItem", "setItem"} {
		t.Run(throwOn, func(t *testing.T) {
			fakeLocalStorage(t, throwOn)
			f := LocalStorageSeqFloor("p:")
			// The floor keeps working from memory, and says so: the value comes with an error
			// that wraps ErrFloorDegraded (the keyring goes on, and notes it).
			if err := f.Raise(ctx, "doc", 5); !errors.Is(err, ErrFloorDegraded) {
				t.Fatalf("a throwing localStorage must be reported: %v", err)
			}
			v, err := f.Get(ctx, "doc")
			if v != 5 || (throwOn != "setItem" && !errors.Is(err, ErrFloorDegraded)) || (throwOn == "setItem" && err != nil) {
				t.Errorf("memory floor: %d %v", v, err) // reads fail with getItem and the accessor, not with setItem
			}
			if f.Err() == nil {
				t.Error("Err should say storage failed")
			}
		})
	}
	// A missing localStorage (a worker) behaves the same way.
	js.Global().Get("Object").Call("defineProperty", js.Global(), "localStorage", map[string]any{"value": js.Undefined(), "configurable": true})
	f := LocalStorageSeqFloor("p:")
	if err := f.Raise(ctx, "doc", 2); !errors.Is(err, ErrFloorDegraded) {
		t.Fatal(err)
	}
	if v, _ := f.Get(ctx, "doc"); v != 2 || f.Err() == nil {
		t.Errorf("%d %v", v, f.Err())
	}
}

// TestLocalStorageFloorRefusesAReplayThroughTheKeyring: the floor as a page would use it. The
// page is "reloaded" by building a new floor over the same storage.
func TestLocalStorageFloorRefusesAReplayThroughTheKeyring(t *testing.T) {
	ctx := context.Background()
	fakeLocalStorage(t, "")
	w := newWorldFloor(t, LocalStorageSeqFloor("p:"))
	id, old := w.confidentialWithOldRecord(t)
	w.replay(t, id, old)
	reloaded := w.ring
	reloaded.People.SeqFloor = LocalStorageSeqFloor("p:")
	if _, _, err := reloaded.Bind(ctx, id+"~aaaaaaaaaaaa.open"); err == nil {
		t.Error("the reloaded page accepted the replayed record")
	}
}
