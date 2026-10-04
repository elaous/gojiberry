package docstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"github.com/elaous/gojiberry/guard"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/elaous/gojiberry/auth"
)

// fakeOIDCProvider mirrors gojiberry/auth's own test helper of the
// same name (auth/server_test.go) -- duplicated here rather than
// exported from that package, since it's test-only scaffolding with
// no real caller outside a test file.
type fakeOIDCProvider struct {
	srv        *httptest.Server
	oidcServer *oidctest.Server
	priv       *rsa.PrivateKey
	keyID      string
	nextEmail  string
	nextSub    string
	nonce      string // echoed into the id_token, like a real provider
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	f := &fakeOIDCProvider{priv: priv, keyID: "test-key"}
	f.oidcServer = &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: priv.Public(), KeyID: f.keyID, Algorithm: oidc.RS256},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.handleToken)
	mux.Handle("/.well-known/openid-configuration", f.oidcServer)
	mux.Handle("/keys", f.oidcServer)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.oidcServer.SetIssuer(f.srv.URL)
	return f
}

func (f *fakeOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	claims := fmt.Sprintf(`{
		"iss": %q,
		"aud": "test-client",
		"sub": %q,
		"exp": %d,
		"iat": %d,
		"email": %q,
		"name": "Test User",
		"nonce": %q
	}`, f.srv.URL, f.nextSub, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), f.nextEmail, f.nonce)
	idToken := oidctest.SignIDToken(f.priv, f.keyID, oidc.RS256, claims)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"id_token":     idToken,
	})
}

func newTestServerWithAuth(t *testing.T, issuerURL string) *Server {
	t.Helper()
	m, err := auth.NewManager(context.Background(), auth.Config{
		BaseURL: "https://app.example.com",
		Providers: []auth.Provider{
			{Name: "test", IssuerURL: issuerURL, ClientID: "test-client", ClientSecret: "test-secret"},
		},
	})
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	return NewServer(NewMemoryAdapter(), "memory").WithAuth(m)
}

// sessionTokenFor drives a real login → callback round trip against
// srv's own mounted /auth/* routes (auth.Manager.Routes, via
// Server.Handler) to mint a genuine bearer session token for email --
// the same mechanism a real browser goes through, not a shortcut that
// only proves the ACL logic works in isolation from the auth it's
// actually built on.
func sessionTokenFor(t *testing.T, handler http.Handler, provider *fakeOIDCProvider, email, sub string) string {
	t.Helper()
	provider.nextEmail, provider.nextSub = email, sub

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/test/login", nil)
	loginW := httptest.NewRecorder()
	handler.ServeHTTP(loginW, loginReq)
	loc, err := url.Parse(loginW.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing login Location: %v", err)
	}
	state := loc.Query().Get("state")
	provider.nonce = loc.Query().Get("nonce")

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	callbackW := httptest.NewRecorder()
	handler.ServeHTTP(callbackW, callbackReq)
	if callbackW.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body: %s", callbackW.Code, callbackW.Body.String())
	}
	callbackLoc := callbackW.Header().Get("Location")
	i := strings.Index(callbackLoc, "#auth_token=")
	if i < 0 {
		t.Fatalf("callback Location %q missing #auth_token=", callbackLoc)
	}
	return callbackLoc[i+len("#auth_token="):]
}

func withBearer(req *http.Request, token string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestDocWithNoACLIsFullyOpen(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()

	put := httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT (no ACL, no token) status = %d, want 204", rec.Code)
	}

	get := httptest.NewRequest(http.MethodGet, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("GET (no ACL, no token) = %d %q, want 200 \"hello\"", rec.Code, rec.Body.String())
	}
}

func TestACLBootstrapsOwnerOnFirstPUT(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	ownerToken := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")

	body := `{"owner":"someone-else@example.com","grants":{"editor@example.com":"editor"}}`
	put := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(body)), ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT acl status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	get := withBearer(httptest.NewRequest(http.MethodGet, "/docs/doc1/acl", nil), ownerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	var got ACL
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding ACL: %v", err)
	}
	if got.Owner != "owner@example.com" {
		t.Fatalf("Owner = %q, want the caller's own email (owner@example.com), never whatever the request body claimed", got.Owner)
	}
	if got.Grants["editor@example.com"] != RoleEditor {
		t.Fatalf("Grants = %+v, want editor@example.com: editor preserved from the request body", got.Grants)
	}
}

func TestACLPutRequiresSignIn(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()

	put := httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestACLOnlyOwnerCanReplaceIt(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	ownerToken := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")
	otherToken := sessionTokenFor(t, handler, provider, "other@example.com", "other-sub")

	put1 := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{}`)), ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put1)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner's first PUT status = %d, want 204", rec.Code)
	}

	put2 := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{}`)), otherToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, put2)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner's PUT status = %d, want 403", rec.Code)
	}
}

func TestACLEnforcesReadWritePerRole(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	ownerToken := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")
	editorToken := sessionTokenFor(t, handler, provider, "editor@example.com", "editor-sub")
	viewerToken := sessionTokenFor(t, handler, provider, "viewer@example.com", "viewer-sub")
	strangerToken := sessionTokenFor(t, handler, provider, "stranger@example.com", "stranger-sub")

	// Owner saves the document, then shares it.
	put := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("v1")), ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner's initial PUT status = %d, want 204", rec.Code)
	}
	aclBody := `{"grants":{"editor@example.com":"editor","viewer@example.com":"viewer"}}`
	putACL := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(aclBody)), ownerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, putACL)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT acl status = %d, want 204", rec.Code)
	}

	tests := []struct {
		name       string
		token      string
		method     string
		wantStatus int
	}{
		{"owner can GET", ownerToken, http.MethodGet, http.StatusOK},
		{"owner can PUT", ownerToken, http.MethodPut, http.StatusNoContent},
		{"editor can GET", editorToken, http.MethodGet, http.StatusOK},
		{"editor can PUT", editorToken, http.MethodPut, http.StatusNoContent},
		{"viewer can GET", viewerToken, http.MethodGet, http.StatusOK},
		{"viewer cannot PUT", viewerToken, http.MethodPut, http.StatusForbidden},
		{"stranger cannot GET", strangerToken, http.MethodGet, http.StatusForbidden},
		{"stranger cannot PUT", strangerToken, http.MethodPut, http.StatusForbidden},
		{"anonymous cannot GET", "", http.MethodGet, http.StatusUnauthorized},
		{"anonymous cannot PUT", "", http.MethodPut, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.method == http.MethodPut {
				req = httptest.NewRequest(tt.method, "/docs/doc1", strings.NewReader("v2"))
			} else {
				req = httptest.NewRequest(tt.method, "/docs/doc1", nil)
			}
			if tt.token != "" {
				req = withBearer(req, tt.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestACLViewerCannotDelete(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	ownerToken := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")
	viewerToken := sessionTokenFor(t, handler, provider, "viewer@example.com", "viewer-sub")

	put := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("v1")), ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	putACL := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{"grants":{"viewer@example.com":"viewer"}}`)), ownerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, putACL)

	del := withBearer(httptest.NewRequest(http.MethodDelete, "/docs/doc1", nil), viewerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer's DELETE status = %d, want 403", rec.Code)
	}

	ownerDel := withBearer(httptest.NewRequest(http.MethodDelete, "/docs/doc1", nil), ownerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, ownerDel)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner's DELETE status = %d, want 204", rec.Code)
	}
}

func TestACLKeyspaceHiddenFromListAndDirectAccess(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	ownerToken := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")

	put := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("v1")), ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	putACL := withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{}`)), ownerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, putACL)

	list := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if strings.Contains(rec.Body.String(), "acl:") {
		t.Fatalf("list body = %q, should never expose the reserved acl: keyspace", rec.Body.String())
	}

	directGet := httptest.NewRequest(http.MethodGet, "/docs/acl:doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, directGet)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("direct GET of a reserved id status = %d, want 400", rec.Code)
	}

	// The shared-editing hub keeps its documents under collab:, behind its
	// own access control; they are not reachable here.
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, "/docs/collab:doc1", strings.NewReader("x")))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s of a hub document status = %d, want 400", method, rec.Code)
		}
	}
}

func TestACLGetForUnsharedDocumentIsEmpty(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	handler := srv.Handler()
	token := sessionTokenFor(t, handler, provider, "someone@example.com", "someone-sub")

	get := withBearer(httptest.NewRequest(http.MethodGet, "/docs/never-shared/acl", nil), token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got ACL
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Owner != "" || len(got.Grants) != 0 {
		t.Fatalf("ACL = %+v, want the empty zero value for a never-shared document", got)
	}
}

func TestACLRoutesRequireAuthConfigured(t *testing.T) {
	srv := newTestServer() // no auth.Manager attached
	handler := srv.Handler()

	get := httptest.NewRequest(http.MethodGet, "/docs/doc1/acl", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// aclFailAdapter fails loading acl: keys other than with ErrNotFound.
type aclFailAdapter struct {
	Adapter
	fail bool
}

func (a *aclFailAdapter) Load(ctx context.Context, id string) (Document, error) {
	if a.fail && strings.HasPrefix(id, aclKeyPrefix) {
		return Document{}, fmt.Errorf("backend down")
	}
	return a.Adapter.Load(ctx, id)
}

func TestACLFailsClosedWhenItCannotBeRead(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	m, err := auth.NewManager(context.Background(), auth.Config{
		BaseURL:   "https://app.example.com",
		Providers: []auth.Provider{{Name: "test", IssuerURL: provider.srv.URL, ClientID: "test-client", ClientSecret: "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &aclFailAdapter{Adapter: NewMemoryAdapter()}
	_ = store.Save(context.Background(), "private", []byte("secret"))
	handler := NewServer(store, "memory").WithAuth(m).Handler()
	get := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/private", nil))
		return rec.Code
	}
	if get() != http.StatusOK {
		t.Fatal("a document with no ACL is open")
	}
	store.fail = true
	if code := get(); code == http.StatusOK {
		t.Fatalf("an unreadable ACL opened the document (status %d)", code)
	}
}

func TestPutBodyIsLimitedAndBackendErrorsAreNotEchoed(t *testing.T) {
	handler := NewServer(NewMemoryAdapter(), "memory").Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/docs/big", io.LimitReader(zeroReader{}, maxDocBytes+1)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized PUT: status %d, want 413", rec.Code)
	}
	rec = httptest.NewRecorder()
	serverError(rec, fmt.Errorf("dial postgres://user:pw@db/x"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("backend text leaked: %d %q", rec.Code, rec.Body.String())
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestACLEndpointFailsClosedAndNeedsAnEmail(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	m, err := auth.NewManager(context.Background(), auth.Config{
		BaseURL:   "https://app.example.com",
		Providers: []auth.Provider{{Name: "test", IssuerURL: provider.srv.URL, ClientID: "test-client", ClientSecret: "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &aclFailAdapter{Adapter: NewMemoryAdapter()}
	handler := NewServer(store, "memory").WithAuth(m).Handler()
	owner := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")
	put := func(token string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc/acl", strings.NewReader(`{}`)), token))
		return rec.Code
	}
	if code := put(owner); code != http.StatusNoContent {
		t.Fatalf("owner bootstrap: %d", code)
	}
	other := sessionTokenFor(t, handler, provider, "other@example.com", "other-sub")
	store.fail = true // the ACL can no longer be read
	if code := put(other); code == http.StatusNoContent {
		t.Fatal("an unreadable ACL let another user take ownership")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withBearer(httptest.NewRequest(http.MethodGet, "/docs/doc/acl", nil), other))
	if rec.Code == http.StatusOK {
		t.Fatal("GET of an unreadable ACL answered as if it were empty")
	}
	store.fail = false
	nobody := sessionTokenFor(t, handler, provider, "", "no-email-sub")
	if code := put(nobody); code != http.StatusForbidden {
		t.Fatalf("an account with no e-mail managed an ACL: %d", code)
	}
}

func TestGuardTokenUsesItsOwnHeader(t *testing.T) {
	h := NewServer(NewMemoryAdapter(), "memory").WithGuard(guard.Token("tok", guard.Options{TokenHeader: "X-Gojiberry-Token"})).Handler()
	do := func(hdr, val string) int {
		req := httptest.NewRequest(http.MethodGet, "/docs", nil)
		if hdr != "" {
			req.Header.Set(hdr, val)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if do("", "") != http.StatusUnauthorized || do("Authorization", "Bearer tok") != http.StatusUnauthorized || do("X-Gojiberry-Token", "tok") != http.StatusOK {
		t.Fatal("the guard token travels in its own header, leaving Authorization to sign-in")
	}
}

func TestACLRoleOf(t *testing.T) {
	a := ACL{Owner: "o@x", Grants: map[string]Role{"e@x": RoleEditor, "v@x": RoleViewer}}
	for email, want := range map[string]string{"o@x": "owner", "e@x": "editor", "v@x": "viewer", "n@x": "", "": ""} {
		if got := a.RoleOf(email); got != want {
			t.Errorf("RoleOf(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestRequireACLClosesUnownedDocumentsAndLetsTheFirstWriterOwnThem(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL).WithRequireACL(true)
	h := srv.Handler()
	do := func(method, token string) int {
		req := httptest.NewRequest(method, "/docs/new", strings.NewReader("data"))
		if token != "" {
			withBearer(req, token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	ana := sessionTokenFor(t, h, provider, "ana@example.com", "ana")
	bo := sessionTokenFor(t, h, provider, "bo@example.com", "bo")
	if do(http.MethodPut, "") != http.StatusUnauthorized || do(http.MethodGet, ana) != http.StatusForbidden {
		t.Fatal("an unowned document is closed to anonymous callers and cannot be read before it exists")
	}
	if do(http.MethodPut, ana) != http.StatusNoContent {
		t.Fatal("the first signed-in writer creates it")
	}
	if do(http.MethodGet, ana) != http.StatusOK || do(http.MethodGet, bo) == http.StatusOK || do(http.MethodPut, bo) == http.StatusNoContent {
		t.Fatal("and owns it: others are refused")
	}
}

func TestACLGrantRevokePeople(t *testing.T) {
	a := ACL{Owner: "me@x.test"}
	for _, bad := range []struct {
		who  string
		role Role
	}{{"", RoleEditor}, {"me@x.test", RoleEditor}, {"a@x.test", "admin"}} {
		if err := a.Grant(bad.who, bad.role); err == nil {
			t.Errorf("granted %+v", bad)
		}
	}
	_ = a.Grant("zed@x.test", RoleViewer)
	_ = a.Grant("ana@x.test", RoleEditor)
	_ = a.Grant("zed@x.test", RoleEditor) // changes the role
	p := a.People()
	if len(p) != 3 || p[0].Role != "owner" || p[1].ID != "ana@x.test" || p[2].Role != "editor" {
		t.Errorf("%+v", p)
	}
	if err := a.Revoke("me@x.test"); err == nil {
		t.Error("the owner cannot be revoked")
	}
	_ = a.Revoke("ana@x.test")
	if a.RoleOf("ana@x.test") != "" || len(a.People()) != 2 {
		t.Errorf("%+v", a)
	}
}
