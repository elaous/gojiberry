//go:build js && wasm

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
)

// fakeBrowser makes navigator.credentials.get answer with a credential whose PRF results are
// built by the JavaScript source js (an expression); get is what the credential is.
func fakeBrowser(t *testing.T, credential string) {
	t.Helper()
	g := js.Global()
	nav := g.Get("Function").New("return { credentials: { get: function() { return Promise.resolve(" + credential + "); } } };").Invoke()
	desc := g.Get("Object").New()
	desc.Set("value", nav)
	desc.Set("configurable", true)
	g.Get("Object").Call("defineProperty", g, "navigator", desc)
}

func TestDeriveKeyOutcomes(t *testing.T) {
	ctx := context.Background()
	cred := func(prf string) string {
		return "{ id: 'cred-1', getClientExtensionResults: function() { return " + prf + "; } }"
	}
	for _, tc := range []struct {
		name, prf string
		want      error
	}{
		{"no prf at all", "{}", ErrNoPRF},
		{"prf not enabled", "{ prf: { enabled: false } }", ErrNoPRF},
		{"no results", "{ prf: { enabled: true } }", ErrNoPRF},
		{"no first", "{ prf: { results: {} } }", ErrNoPRF},
		{"too short", "{ prf: { results: { first: new Uint8Array(16).buffer } } }", ErrPRFLength},
		{"too long", "{ prf: { results: { first: new Uint8Array(33).buffer } } }", ErrPRFLength},
	} {
		fakeBrowser(t, cred(tc.prf))
		id, secret, err := DeriveKey(ctx, "", []byte("salt"))
		if !errors.Is(err, tc.want) || secret != nil || id != "" {
			t.Errorf("%s: %q %v %v", tc.name, id, secret, err)
		}
	}
	if errors.Is(ErrNoPRF, ErrPRFLength) || errors.Is(ErrPRFLength, ErrNoPRF) {
		t.Error("the two are told apart")
	}
	// A good answer is returned, and the browser's own buffer is zeroed after the copy.
	g := js.Global()
	buf := g.Get("Uint8Array").New(32)
	for i := 0; i < 32; i++ {
		buf.SetIndex(i, i+1)
	}
	g.Set("testPRFBuffer", buf.Get("buffer"))
	fakeBrowser(t, cred("{ prf: { results: { first: testPRFBuffer } } }"))
	id, secret, err := DeriveKey(ctx, "", []byte("salt"))
	if err != nil || id != "cred-1" || len(secret) != 32 || secret[0] != 1 || secret[31] != 32 {
		t.Fatalf("%q %v %v", id, secret, err)
	}
	for i := 0; i < 32; i++ {
		if buf.Index(i).Int() != 0 {
			t.Fatalf("byte %d of the browser's buffer was not zeroed", i)
		}
	}
	// The same through a typed-array view (an offset into a larger buffer).
	big := g.Get("Uint8Array").New(64)
	for i := 0; i < 64; i++ {
		big.SetIndex(i, 9)
	}
	g.Set("testPRFView", g.Get("Uint8Array").New(big.Get("buffer"), 16, 32))
	fakeBrowser(t, cred("{ prf: { results: { first: testPRFView } } }"))
	if _, secret, err := DeriveKey(ctx, "", nil); err != nil || len(secret) != 32 {
		t.Fatalf("%v %v", secret, err)
	}
	if big.Index(16).Int() != 0 || big.Index(47).Int() != 0 || big.Index(15).Int() != 9 || big.Index(48).Int() != 9 {
		t.Error("only the view was zeroed")
	}
	// A bad credential ID hint is refused before the browser is asked.
	if _, _, err := DeriveKey(ctx, "!!not base64!!", nil); err == nil {
		t.Error("bad hint")
	}
}
