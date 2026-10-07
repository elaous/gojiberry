//go:build js && wasm

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"
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

// fakePage gives the node test the page globals CreateLocal reads: a location and a localStorage.
func fakePage(t *testing.T) {
	t.Helper()
	g := js.Global()
	g.Set("location", g.Get("Function").New("return { hostname: 'localhost' };").Invoke())
	g.Set("localStorage", g.Get("Function").New("var m = {}; return { getItem: function(k) { return k in m ? m[k] : null; }, setItem: function(k, v) { m[k] = String(v); }, removeItem: function(k) { delete m[k]; } };").Invoke())
	g.Set("PublicKeyCredential", g.Get("Object").New())
	g.Get("PublicKeyCredential").Set("parseCreationOptionsFromJSON", g.Get("Function").New("return 1"))
	g.Get("PublicKeyCredential").Set("parseRequestOptionsFromJSON", g.Get("Function").New("return 1"))
}

// fakeCredentials replaces navigator.credentials with create and get that run the JavaScript
// function bodies given (each returns a promise).
func fakeCredentials(t *testing.T, create, get string) {
	t.Helper()
	g := js.Global()
	nav := g.Get("Function").New("return { credentials: { create: function() { " + create + " }, get: function() { " + get + " } } };").Invoke()
	desc := g.Get("Object").New()
	desc.Set("value", nav)
	desc.Set("configurable", true)
	g.Get("Object").Call("defineProperty", g, "navigator", desc)
}

// A passkey that was made but cannot do PRF is still in the authenticator: CreateLocal says
// ErrNoPRF and still returns its ID, so the page can say which passkey to delete, and stores
// nothing for it.
func TestCreateLocalNoPRFStillNamesThePasskey(t *testing.T) {
	fakePage(t)
	fakeCredentials(t, "return Promise.resolve({ rawId: new Uint8Array([1,2,3,4]).buffer, getClientExtensionResults: function() { return { prf: { enabled: false } }; } });", "return Promise.reject(new Error('x'));")
	ForgetLocal("t-nopr")
	id, err := CreateLocal(context.Background(), "t-nopr", "rp", "user")
	if !errors.Is(err, ErrNoPRF) || id != "AQIDBA" {
		t.Fatalf("%q %v", id, err)
	}
	if LocalCredential("t-nopr") != "" {
		t.Error("a passkey that cannot encrypt was remembered")
	}
}

func TestRejectionsOfTheBrowserAreClassified(t *testing.T) {
	fakePage(t)
	reject := func(name, msg string) string {
		return "var e = new Error('" + msg + "'); e.name = '" + name + "'; return Promise.reject(e);"
	}
	ctx := context.Background()
	fakeCredentials(t, reject("NotAllowedError", "The operation either timed out or was not allowed."), reject("NotAllowedError", "The operation either timed out or was not allowed."))
	if _, _, err := DeriveKey(ctx, "", []byte("s")); !errors.Is(err, ErrCancelled) || errors.Is(err, ErrTimedOut) {
		t.Errorf("a quick refusal: %v", err)
	}
	old := promptTimeout
	defer func() { promptTimeout = old }()
	promptTimeout = 20 * time.Millisecond
	fakeCredentials(t, "return new Promise(function(r, j) { setTimeout(function() { var e = new Error('The operation either timed out or was not allowed.'); e.name = 'NotAllowedError'; j(e); }, 40); });", "return new Promise(function(r, j) { setTimeout(function() { var e = new Error('The operation either timed out or was not allowed.'); e.name = 'NotAllowedError'; j(e); }, 40); });")
	if _, _, err := DeriveKey(ctx, "", []byte("s")); !errors.Is(err, ErrTimedOut) {
		t.Errorf("a timeout: %v", err)
	}
	if _, err := CreateLocal(ctx, "t-x", "rp", "user"); !errors.Is(err, ErrTimedOut) {
		t.Errorf("a timeout on create: %v", err)
	}
	fakeCredentials(t, reject("SecurityError", "insecure"), reject("SecurityError", "insecure"))
	if _, _, err := DeriveKey(ctx, "", []byte("s")); !errors.Is(err, ErrInsecureContext) {
		t.Errorf("a security error: %v", err)
	}
}

func TestSetLocalCredentialPutsAnEarlierPasskeyBack(t *testing.T) {
	fakePage(t)
	SetLocalCredential("t-slot", "first")
	if LocalCredential("t-slot") != "first" {
		t.Fatal("not stored")
	}
	SetLocalCredential("t-slot", "second") // a new passkey was made
	SetLocalCredential("t-slot", "first")  // and the change that needed it failed
	if LocalCredential("t-slot") != "first" {
		t.Fatal("not put back")
	}
	SetLocalCredential("t-slot", "")
	if LocalCredential("t-slot") != "" {
		t.Fatal("not forgotten")
	}
}
