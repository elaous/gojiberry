package encrypt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

type dirEnv struct {
	t     *testing.T
	store docstore.Adapter
	h     http.Handler
	mgr   *auth.Manager
	dir   *AdapterDirectory
}

func newDirEnv(t *testing.T) *dirEnv { return newDirEnvWith(t, nil, nil) }

func newDirEnvWith(t *testing.T, dir func(*AdapterDirectory), srvOpt func(*docstore.Server)) *dirEnv {
	t.Helper()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: store, Signer: testSigner(t, 1)}
	if dir != nil {
		dir(d)
	}
	srv := docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(DirectoryService(d))
	if srvOpt != nil {
		srvOpt(srv)
	}
	return &dirEnv{t: t, store: store, h: srv.Handler(), mgr: mgr, dir: d}
}

func (e *dirEnv) token(u auth.User) string {
	tok, err := e.mgr.StartSession(u)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *dirEnv) do(method, path, tok, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *dirEnv) publish(tok string, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return e.do("POST", "/directory/publish", tok, string(b))
}

func (e *dirEnv) lookup(tok, email string) (int, []docstore.DirectoryKey) {
	rec := e.do("GET", "/directory/lookup?email="+email, tok, "")
	var out struct{ Keys []docstore.DirectoryKey }
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatal(err)
		}
	}
	return rec.Code, out.Keys
}

func TestDirectoryRoutesPublishAndLookup(t *testing.T) {
	e := newDirEnv(t)
	ana := e.token(auth.User{ID: "google:1", Email: "Ana@X.io", VerifiedEmail: true})
	key := newRecovery(t)
	rec := e.publish(ana, map[string]string{"key": key.Text, "label": "laptop"})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	pk := e.token(auth.User{ID: "passkey:z"})
	if code, _ := e.lookup(pk, "ana@x.io"); code != 403 {
		t.Errorf("passkey lookup = %d, want 403", code)
	}
	bo := e.token(auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true})
	for _, tok := range []string{ana, bo} {
		code, keys := e.lookup(tok, "ana@x.io")
		if code != 200 || len(keys) != 1 || !keys[0].Verified || keys[0].Fingerprint != key.Fingerprint || keys[0].Label != "laptop" || keys[0].Key != key.Text {
			t.Fatalf("%d %+v", code, keys)
		}
	}
	if code, keys := e.lookup(ana, "nobody@x.io"); code != 200 || keys == nil || len(keys) != 0 {
		t.Errorf("%d %v: an empty list, not null", code, keys)
	}
	if code, _ := e.lookup("", "ana@x.io"); code != 401 {
		t.Errorf("lookup signed out = %d", code)
	}
	if code, _ := e.lookup(ana, ""); code != 400 {
		t.Errorf("lookup without an address = %d", code)
	}
	if rec := e.do("POST", "/directory/lookup", ana, ""); rec.Code != 405 {
		t.Errorf("%d", rec.Code)
	}
	if rec := e.do("GET", "/directory/publish", ana, ""); rec.Code != 405 {
		t.Errorf("%d", rec.Code)
	}
	metas, _ := docstore.ListPrefix(context.Background(), e.store, "directory/")
	if len(metas) != 1 {
		t.Fatalf("%v", metas)
	}
	if rec := e.do("GET", "/docs/"+metas[0].ID, ana, ""); rec.Code != 400 {
		t.Errorf("read of a record = %d", rec.Code)
	}
	if rec := e.do("PUT", "/docs/"+metas[0].ID, ana, "forged"); rec.Code != 400 {
		t.Errorf("write of a record = %d", rec.Code)
	}
}

func TestDirectoryPublishRefusals(t *testing.T) {
	e := newDirEnv(t)
	key := newRecovery(t)
	body := map[string]string{"key": key.Text}
	if rec := e.publish("", body); rec.Code != 401 {
		t.Errorf("signed out = %d", rec.Code)
	}
	if rec := e.publish("not-a-session", body); rec.Code != 401 {
		t.Errorf("bad token = %d", rec.Code)
	}
	for name, u := range map[string]auth.User{
		"no email":    {ID: "google:1"},
		"unverified":  {ID: "google:1", Email: "ana@x.io"},
		"passkey":     {ID: "passkey:1", Email: "ana@x.io", VerifiedEmail: true},
		"blank email": {ID: "google:1", Email: " "},
	} {
		if rec := e.publish(e.token(u), body); rec.Code != 403 {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	ana := e.token(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	for name, b := range map[string]string{"not json": "{", "no key": `{"label":"x"}`, "bad key": `{"key":"garbage"}`} {
		want := 400
		if rec := e.do("POST", "/directory/publish", ana, b); rec.Code != want {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := e.publish(ana, map[string]string{"key": key.Text, "email": "bo@x.io"}); rec.Code != 403 {
		t.Errorf("another address = %d", rec.Code)
	}
	if code, keys := e.lookup(ana, "bo@x.io"); code != 200 || len(keys) != 0 {
		t.Errorf("nothing was published for bo: %v", keys)
	}
	if rec := e.publish(ana, map[string]string{"key": key.Text, "email": " ANA@x.io "}); rec.Code != 200 {
		t.Errorf("own address = %d", rec.Code)
	}
	big := `{"key":"` + strings.Repeat("a", 8<<10) + `"}`
	if rec := e.do("POST", "/directory/publish", ana, big); rec.Code != 413 {
		t.Errorf("big body = %d", rec.Code)
	}
}

func TestDirectoryRoutesAreLimitedPerUser(t *testing.T) {
	e := newDirEnv(t)
	ana := e.token(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	bo := e.token(auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true})
	var last int
	for i := 0; i < 11; i++ {
		last = e.publish(ana, map[string]string{"key": newRecovery(t).Text}).Code
	}
	if last != 429 {
		t.Errorf("11th publish = %d", last)
	}
	if rec := e.publish(bo, map[string]string{"key": newRecovery(t).Text}); rec.Code != 200 {
		t.Errorf("another user is not limited: %d", rec.Code)
	}
	for i := 0; i < 61; i++ {
		last, _ = e.lookup(ana, "bo@x.io")
	}
	if last != 429 {
		t.Errorf("61st lookup = %d", last)
	}
}

func TestDirectoryRoutesNeedSignInAndAService(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	d := DirectoryService(&AdapterDirectory{Store: store, Signer: testSigner(t, 1)})
	h := docstore.NewServer(store, "memory").WithDirectory(d).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/directory/lookup?email=a@x.io", nil))
	if rec.Code != 404 {
		t.Errorf("without sign-in: %d", rec.Code)
	}
	mgr, _ := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	h = docstore.NewServer(store, "memory").WithAuth(mgr).Handler()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/directory/lookup?email=a@x.io", nil))
	if rec.Code != 404 {
		t.Errorf("without a directory: %d", rec.Code)
	}
}

func TestDirectoryServiceMapsRefusals(t *testing.T) {
	svc := DirectoryService(&AdapterDirectory{Store: docstore.NewMemoryAdapter()}) // no signer: off
	_, err := svc.Publish(context.Background(), auth.User{ID: "google:1", Email: "a@x.io", VerifiedEmail: true}, docstore.DirectoryPublish{Key: newRecovery(t).Text})
	if err == nil || !strings.Contains(err.Error(), "refuses") {
		t.Errorf("%v", err)
	}
}

func TestDirectoryLookupIsLimitedPerIP(t *testing.T) {
	e := newDirEnv(t)
	var toks []string
	for i := 1; i <= 3; i++ {
		toks = append(toks, e.token(auth.User{ID: fmt.Sprintf("google:%d", i), Email: fmt.Sprintf("u%d@x.io", i), VerifiedEmail: true}))
	}
	var last int
	for i := 0; i < 121; i++ {
		last, _ = e.lookup(toks[i%3], "bo@x.io")
	}
	if last != 429 {
		t.Errorf("121st lookup from one address = %d, want 429", last)
	}
}

func TestDirectoryRouteShowsAForgedRecordUnverified(t *testing.T) {
	e := newDirEnv(t)
	ana := e.token(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	d := &AdapterDirectory{Store: e.store, Signer: testSigner(t, 1)}
	key := newRecovery(t)
	id := d.directoryID("ana@x.io", key.Fingerprint)
	forged, _ := json.Marshal(directoryRecord{V: 2, Key: key.Text, Label: "mallory", Publisher: "google:9", Provider: "google", Email: "ana@x.io", At: 1, Sig: "AAAA"})
	if err := e.store.Save(context.Background(), id, forged); err != nil {
		t.Fatal(err)
	}
	code, keys := e.lookup(ana, "ana@x.io")
	if code != 200 || len(keys) != 1 || keys[0].Verified || keys[0].Label != "mallory" {
		t.Fatalf("%d %+v: a forged record must come back unverified", code, keys)
	}
	unsigned, _ := json.Marshal(directoryRecord{V: 2, Key: key.Text, Provider: "google", Publisher: "google:9", Email: "ana@x.io"})
	_ = e.store.Save(context.Background(), id, unsigned)
	if _, keys := e.lookup(ana, "ana@x.io"); len(keys) != 1 || keys[0].Verified {
		t.Fatalf("%+v", keys)
	}
}
