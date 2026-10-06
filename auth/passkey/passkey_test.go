package passkey

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/webauthntest"
)

const (
	rpID   = "opendocs.io"
	origin = "https://opendocs.io"
)

type rig struct {
	t    *testing.T
	svc  *Service
	mgr  *auth.Manager
	srv  *httptest.Server
	user *webauthntest.Authenticator
}

func newRig(t *testing.T, store Store) *rig {
	t.Helper()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: origin})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{RPID: rpID, RPName: "opendocs", Origins: []string{origin}}, store, mgr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/passkey/", svc.Handler())
	mux.Handle("/auth/", mgr.Routes())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rig{t: t, svc: svc, mgr: mgr, srv: srv, user: webauthntest.New(rpID)}
}

func (r *rig) do(method, path, token string, body []byte) (int, map[string]any) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	m, _ := out.(map[string]any)
	if m == nil {
		m = map[string]any{"list": out}
	}
	return resp.StatusCode, m
}

func asJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// register makes an account for email with the rig's authenticator and returns the session token.
func (r *rig) register(email, signedIn string) string {
	r.t.Helper()
	code, begin := r.do("POST", "/auth/passkey/register/begin", signedIn, asJSON(r.t, map[string]string{"email": email, "name": "Ana"}))
	if code != 200 {
		r.t.Fatalf("begin: %d %v", code, begin)
	}
	answer, err := r.user.Create(asJSON(r.t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	if err != nil {
		r.t.Fatal(err)
	}
	code, fin := r.do("POST", "/auth/passkey/register/finish?label=Laptop&token="+begin["token"].(string), signedIn, answer)
	if code != 200 {
		r.t.Fatalf("finish: %d %v", code, fin)
	}
	tok, _ := fin["auth_token"].(string)
	return tok
}

func (r *rig) login() (int, map[string]any) {
	r.t.Helper()
	code, begin := r.do("POST", "/auth/passkey/login/begin", "", nil)
	if code != 200 {
		r.t.Fatalf("begin: %d %v", code, begin)
	}
	answer, err := r.user.Get(asJSON(r.t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.do("POST", "/auth/passkey/login/finish?token="+begin["token"].(string), "", answer)
}

func TestRegisterWithAPasskeyAndSignInLaterWithoutTyping(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	tok := r.register("ana@opendocs.io", "")
	if tok == "" {
		t.Fatal("a new account is signed in when its passkey is made")
	}
	if code, me := r.do("GET", "/auth/me", tok, nil); code != 200 || me["Email"] != "ana@opendocs.io" || !strings.HasPrefix(me["ID"].(string), "passkey:") {
		t.Fatalf("%d %v", code, me)
	}
	// Later: no user name, the authenticator offers the account.
	code, out := r.login()
	if code != 200 || out["auth_token"] == "" || out["user"].(map[string]any)["Email"] != "ana@opendocs.io" {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := r.do("GET", "/auth/me", out["auth_token"].(string), nil); code != 200 {
		t.Errorf("the session is an ordinary one: %d", code)
	}
	// The counter was raised and the use recorded.
	cs, _ := r.svc.Credentials(context.Background(), out["user"].(map[string]any)["ID"].(string))
	if len(cs) != 1 || cs[0].Cred.Authenticator.SignCount != 1 || cs[0].LastUsed.IsZero() || cs[0].Label != "Laptop" {
		t.Errorf("%+v", cs)
	}
}

func TestAChallengeIsGoodForOneFinishAndExpires(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	r.register("ana@opendocs.io", "")
	_, begin := r.do("POST", "/auth/passkey/login/begin", "", nil)
	answer, _ := r.user.Get(asJSON(t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	path := "/auth/passkey/login/finish?token=" + begin["token"].(string)
	if code, _ := r.do("POST", path, "", answer); code != 200 {
		t.Fatal(code)
	}
	if code, _ := r.do("POST", path, "", answer); code != http.StatusGone {
		t.Errorf("a replayed sign-in: %d", code)
	}
	// An answer to a challenge of another start is refused.
	_, b1 := r.do("POST", "/auth/passkey/login/begin", "", nil)
	_, b2 := r.do("POST", "/auth/passkey/login/begin", "", nil)
	ans1, _ := r.user.Get(asJSON(t, map[string]any{"publicKey": b1["options"].(map[string]any)["publicKey"]}), origin)
	if code, _ := r.do("POST", "/auth/passkey/login/finish?token="+b2["token"].(string), "", ans1); code != http.StatusUnauthorized {
		t.Errorf("the wrong challenge: %d", code)
	}
	// Expiry.
	r.svc.ttl = time.Millisecond
	_, b3 := r.do("POST", "/auth/passkey/login/begin", "", nil)
	time.Sleep(20 * time.Millisecond)
	ans3, _ := r.user.Get(asJSON(t, map[string]any{"publicKey": b3["options"].(map[string]any)["publicKey"]}), origin)
	if code, _ := r.do("POST", "/auth/passkey/login/finish?token="+b3["token"].(string), "", ans3); code != http.StatusGone {
		t.Errorf("an expired sign-in: %d", code)
	}
}

func TestWrongOriginUnknownCredentialAndMissingUserVerificationAreRefused(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	r.register("ana@opendocs.io", "")
	// Another site's page answering a sign-in for this one.
	_, begin := r.do("POST", "/auth/passkey/login/begin", "", nil)
	ans, _ := r.user.Get(asJSON(t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), "https://evil.example")
	code, out := r.do("POST", "/auth/passkey/login/finish?token="+begin["token"].(string), "", ans)
	if code != http.StatusUnauthorized || out["auth_token"] != nil {
		t.Errorf("wrong origin: %d %v", code, out)
	}
	if msg, _ := out["error"].(string); strings.Contains(msg, "origin") || strings.Contains(msg, "evil") {
		t.Errorf("the reason is not given away: %q", msg)
	}
	// An authenticator the server has never seen.
	stranger := webauthntest.New(rpID)
	r.user = stranger
	if code, _ := r.login(); code != http.StatusUnauthorized {
		t.Errorf("unknown credential: %d", code)
	}
	// An authenticator that cannot verify the person is not accepted when verification is required.
	r.user = webauthntest.New(rpID)
	r.user.UserVerified = false
	_, bg := r.do("POST", "/auth/passkey/register/begin", "", asJSON(t, map[string]string{"email": "bo@opendocs.io"}))
	if _, err := r.user.Create(asJSON(t, map[string]any{"publicKey": bg["options"].(map[string]any)["publicKey"]}), origin); err == nil {
		t.Error("the options ask for user verification")
	}
	if code, _ := r.do("POST", "/auth/passkey/register/begin", "", asJSON(t, map[string]string{})); code != http.StatusBadRequest {
		t.Errorf("an e-mail address is needed: %d", code)
	}
}

func TestAPasskeyAddedWhileSignedInIsLinkedToThatIdentity(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	oidc, err := r.mgr.StartSession(auth.User{ID: "google:42", Email: "ana@gmail.com", Name: "Ana"})
	if err != nil {
		t.Fatal(err)
	}
	if tok := r.register("", oidc); tok != "" {
		t.Error("already signed in: no new session")
	}
	code, out := r.login()
	if code != 200 || out["user"].(map[string]any)["ID"] != "google:42" {
		t.Fatalf("the passkey signs in as the same identity: %d %v", code, out)
	}
	// A second passkey (another authenticator) can be added and the list shows both.
	r.user = webauthntest.New(rpID)
	r.register("", oidc)
	code, list := r.do("GET", "/auth/passkey/credentials", oidc, nil)
	rows, _ := list["list"].([]any)
	if code != 200 || len(rows) != 2 {
		t.Fatalf("%d %v", code, list)
	}
	if code, _ := r.do("GET", "/auth/passkey/credentials", "", nil); code != http.StatusUnauthorized {
		t.Errorf("the list needs a session: %d", code)
	}
}

func TestRemovingPasskeysKeepsTheLastUnlessThereIsAnotherWay(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	tok := r.register("ana@opendocs.io", "")
	_, me := r.do("GET", "/auth/me", tok, nil)
	id := me["ID"].(string)
	first := r.user.CredentialID()
	r.user = webauthntest.New(rpID)
	r.register("ana@opendocs.io", tok)
	if code, _ := r.do("DELETE", "/auth/passkey/credentials/"+first, tok, nil); code != http.StatusNoContent {
		t.Fatalf("one of two can go: %d", code)
	}
	left := r.user.CredentialID()
	if code, out := r.do("DELETE", "/auth/passkey/credentials/"+left, tok, nil); code != http.StatusConflict {
		t.Fatalf("the last one stays: %d %v", code, out)
	}
	r.svc.HasOtherWay = func(_ context.Context, acct string) bool { return acct == id }
	if code, _ := r.do("DELETE", "/auth/passkey/credentials/"+left, tok, nil); code != http.StatusNoContent {
		t.Errorf("with another way in it can go: %d", code)
	}
	if code, _ := r.do("DELETE", "/auth/passkey/credentials/nope", tok, nil); code != http.StatusNotFound {
		t.Errorf("%d", code)
	}
}

func TestAdapterStoreKeepsAccountsInADocstore(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	r := newRig(t, &AdapterStore{Adapter: mem})
	r.register("ana@opendocs.io", "")
	if code, out := r.login(); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	metas, _ := mem.List(ctx)
	var account, handle int
	for _, m := range metas {
		switch {
		case strings.HasPrefix(m.ID, "passkey/account/"):
			account++
		case strings.HasPrefix(m.ID, "passkey/handle/"):
			handle++
		}
	}
	if account != 1 || handle != 1 {
		t.Errorf("%d accounts %d handles: %v", account, handle, metas)
	}
	st := &AdapterStore{Adapter: mem}
	if _, err := st.Get(ctx, "nobody"); err != ErrNotFound {
		t.Errorf("%v", err)
	}
	if _, err := st.ByHandle(ctx, []byte("nobody")); err != ErrNotFound {
		t.Errorf("%v", err)
	}
	if err := st.Delete(ctx, "nobody"); err != ErrNotFound {
		t.Errorf("%v", err)
	}
}

func TestConfigIsChecked(t *testing.T) {
	mgr, _ := auth.NewManager(context.Background(), auth.Config{BaseURL: origin})
	if _, err := New(Config{RPID: rpID}, nil, mgr); err == nil {
		t.Error("a store is required")
	}
	if _, err := New(Config{RPID: rpID, Origins: []string{origin}}, NewMemoryStore(), nil); err == nil {
		t.Error("a session manager is required")
	}
	if _, err := New(Config{RPID: "", Origins: []string{origin}}, NewMemoryStore(), mgr); err == nil {
		t.Error("an RP ID is required")
	}
}
