package encrypt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func (e *dirEnv) post(path, tok string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return e.do("POST", path, tok, string(b))
}

// prove asks for a challenge for key and answers it.
func (e *dirEnv) prove(tok string, key Recipient, id age.Identity) docstore.DirectoryProof {
	e.t.Helper()
	rec := e.post("/directory/challenge", tok, map[string]string{"key": key.Text})
	if rec.Code != 200 {
		e.t.Fatalf("challenge: %d %s", rec.Code, rec.Body)
	}
	var c docstore.DirectoryChallenge
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatal(err)
	}
	p, err := OpenChallenge(c, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func TestDirectoryRoutesChallengePublishRevokeLogAndLookup(t *testing.T) {
	e := newDirEnv(t)
	anaTok := e.token(ana)
	boTok := e.token(bo)
	pemBytes, key, id := edSSHKey(t)
	_, att, _ := AttestSSH(pemBytes, "ana@x.io", time.Now())

	proof := e.prove(anaTok, key, id)
	rec := e.post("/directory/publish", anaTok, docstore.DirectoryPublish{Key: key.Text, Label: "laptop", Proof: &proof, Attestation: &att})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var pub docstore.DirectoryKey
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	if !pub.Verified || !pub.Held || !pub.Attested || pub.Attestation == nil || pub.Seq != 1 || pub.Kid == "" {
		t.Errorf("%+v", pub)
	}
	// Anyone signed in sees the flags on lookup.
	code, keys := e.lookup(boTok, "ana@x.io")
	if code != 200 || len(keys) != 1 || !keys[0].Attested || !keys[0].Held || !VerifyAttestation(keys[0].Key, "ana@x.io", *keys[0].Attestation) {
		t.Fatalf("%d %+v", code, keys)
	}
	// The same proof cannot publish again (replay), another user's cannot publish at all.
	if rec := e.post("/directory/publish", anaTok, docstore.DirectoryPublish{Key: key.Text, Proof: &proof}); rec.Code != 403 {
		t.Errorf("replayed proof = %d", rec.Code)
	}
	otherKey, otherID := ageKey(t)
	p2 := e.prove(anaTok, otherKey, otherID)
	if rec := e.post("/directory/publish", boTok, docstore.DirectoryPublish{Key: otherKey.Text, Proof: &p2}); rec.Code != 403 {
		t.Errorf("another user's proof = %d", rec.Code)
	}
	badAtt := att
	badAtt.Sig = "AAAA"
	if rec := e.post("/directory/publish", anaTok, docstore.DirectoryPublish{Key: otherKey.Text, Proof: &p2, Attestation: &badAtt}); rec.Code != 400 {
		t.Errorf("bad attestation = %d", rec.Code)
	}

	// The log, for a provider session (a passkey one is refused, see TestDirectoryLogRoutesNeedAProviderSession).
	rec = e.do("GET", "/directory/log?since=0", anaTok, "")
	var lg docstore.DirectoryLog
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &lg) != nil || len(lg.Entries) != 1 || lg.Entries[0].Address != e.dir.LogAddressFor("ana@x.io") || len(lg.Keys) != 1 || !lg.Filtered {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/directory/log?since=1", boTok, ""); !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("empty log is a list: %s", rec.Body)
	}
	if rec := e.do("GET", "/directory/log?since=x", boTok, ""); rec.Code != 400 {
		t.Errorf("since=x: %d", rec.Code)
	}
	if rec := e.do("GET", "/directory/log", "", ""); rec.Code != 401 {
		t.Errorf("signed out: %d", rec.Code)
	}
	if rec := e.do("POST", "/directory/log", boTok, ""); rec.Code != 405 {
		t.Errorf("%d", rec.Code)
	}

	// Revoke: only the owner of the address; the key leaves lookup at once.
	path := "/directory/keys/" + url.PathEscape(key.Fingerprint)
	if rec := e.do("DELETE", path, "", ""); rec.Code != 401 {
		t.Errorf("signed out = %d", rec.Code)
	}
	if rec := e.do("DELETE", path, boTok, ""); rec.Code != 404 {
		t.Errorf("another address = %d", rec.Code)
	}
	if rec := e.do("DELETE", path, e.token(auth.User{ID: "passkey:z", Email: "ana@x.io", VerifiedEmail: true}), ""); rec.Code != 403 {
		t.Errorf("passkey = %d", rec.Code)
	}
	if rec := e.do("GET", path, anaTok, ""); rec.Code != 405 {
		t.Errorf("GET = %d", rec.Code)
	}
	if rec := e.do("DELETE", "/directory/keys/", anaTok, ""); rec.Code != 400 {
		t.Errorf("no fingerprint = %d", rec.Code)
	}
	rec = e.do("DELETE", path, anaTok, "")
	var rev docstore.DirectoryKey
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rev) != nil || !rev.Revoked || rev.Fingerprint != key.Fingerprint {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if code, keys := e.lookup(boTok, "ana@x.io"); code != 200 || len(keys) != 0 {
		t.Errorf("revoked key still found: %v", keys)
	}
	rec = e.do("GET", "/directory/lookup?email=ana@x.io&revoked=1", boTok, "")
	var out struct{ Keys []docstore.DirectoryKey }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Keys) != 1 || !out.Keys[0].Revoked || out.Keys[0].RevokedAt == 0 {
		t.Errorf("revoked=1: %s", rec.Body)
	}
	if rec := e.do("DELETE", path, anaTok, ""); rec.Code != 404 {
		t.Errorf("twice = %d", rec.Code)
	}
	// A replayed publish request (no proof asked for) cannot bring it back within the window.
	if rec := e.post("/directory/publish", anaTok, map[string]string{"key": key.Text}); rec.Code != 409 {
		t.Errorf("replayed publish = %d", rec.Code)
	}
	// The path form of a fingerprint revokes as well (no "/" to be cleaned out of a URL).
	other := newRecovery(t)
	e.publish(anaTok, map[string]string{"key": other.Text})
	if rec := e.do("DELETE", "/directory/keys/"+docstore.DirectoryFingerprintSegment(other.Fingerprint), anaTok, ""); rec.Code != 200 {
		t.Errorf("path form = %d %s", rec.Code, rec.Body)
	}
	// The log is filtered to the caller's own address (a stranger sees none of ana's entries).
	if rec := e.do("GET", "/directory/log", boTok, ""); !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("another address's entries in the log: %s", rec.Body)
	}
	lg2 := e.do("GET", "/directory/log", anaTok, "")
	_ = json.Unmarshal(lg2.Body.Bytes(), &lg)
	var ops []string
	for _, en := range lg.Entries {
		ops = append(ops, en.Op)
	}
	if strings.Join(ops, ",") != "publish,revoke,publish,revoke" {
		t.Errorf("log ops: %v", ops)
	}
}

func TestDirectoryRoutesChallengeRefusals(t *testing.T) {
	e := newDirEnvWith(t, func(d *AdapterDirectory) { d.RequireProof = true }, nil)
	anaTok := e.token(ana)
	key, id := ageKey(t)
	if rec := e.publish(anaTok, map[string]string{"key": key.Text}); rec.Code != 428 {
		t.Errorf("no proof where one is required = %d", rec.Code)
	}
	if rec := e.do("GET", "/directory/challenge", anaTok, ""); rec.Code != 405 {
		t.Errorf("%d", rec.Code)
	}
	if rec := e.post("/directory/challenge", "", map[string]string{"key": key.Text}); rec.Code != 401 {
		t.Errorf("%d", rec.Code)
	}
	for name, u := range map[string]auth.User{"passkey": {ID: "passkey:1", Email: "a@x.io", VerifiedEmail: true}, "unverified": {ID: "google:3", Email: "a@x.io"}} {
		if rec := e.post("/directory/challenge", e.token(u), map[string]string{"key": key.Text}); rec.Code != 403 {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := e.post("/directory/challenge", anaTok, map[string]string{"key": "garbage"}); rec.Code != 400 {
		t.Errorf("garbage key = %d", rec.Code)
	}
	if rec := e.post("/directory/challenge", anaTok, map[string]string{}); rec.Code != 400 {
		t.Errorf("no key = %d", rec.Code)
	}
	if rec := e.do("POST", "/directory/challenge", anaTok, "{"); rec.Code != 400 {
		t.Errorf("not json = %d", rec.Code)
	}
	proof := e.prove(anaTok, key, id)
	if rec := e.post("/directory/publish", anaTok, docstore.DirectoryPublish{Key: key.Text, Proof: &proof}); rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	// The challenge route is limited per user, and the limit is not the publish one.
	var last int
	for range 21 {
		last = e.post("/directory/challenge", anaTok, map[string]string{"key": key.Text}).Code
	}
	if last != 429 {
		t.Errorf("21st challenge = %d", last)
	}
	if rec := e.post("/directory/challenge", e.token(bo), map[string]string{"key": key.Text}); rec.Code != 200 {
		t.Errorf("another user is not limited: %d", rec.Code)
	}
}

func TestDirectoryRoutesShareLimitsThroughTheStore(t *testing.T) {
	mgr, _ := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	store := docstore.NewMemoryAdapter()
	signer := testSigner(t, 1)
	replica := func() http.Handler {
		d := &AdapterDirectory{Store: store, Signer: signer}
		return docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(DirectoryService(d)).
			WithDirectoryLimits(docstore.StoreLimiters(store)).Handler()
	}
	a, b := replica(), replica()
	tok, _ := mgr.StartSession(ana)
	publish := func(h http.Handler) int {
		body, _ := json.Marshal(map[string]string{"key": newRecovery(t).Text})
		req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	// Ten publishes a minute for the user, spread over two replicas: the 11th is refused
	// wherever it lands (a per-process budget would have allowed 20).
	for i := range 10 {
		h := a
		if i%2 == 1 {
			h = b
		}
		if code := publish(h); code != 200 {
			t.Fatalf("publish %d = %d", i, code)
		}
	}
	if code := publish(a); code != 429 {
		t.Errorf("11th via a = %d", code)
	}
	if code := publish(b); code != 429 {
		t.Errorf("11th via b = %d", code)
	}
	// The budget records live in the reserved keyspace and not in the document API.
	metas, _ := docstore.ListPrefix(context.Background(), store, docstore.RateLimitPrefix)
	if len(metas) == 0 {
		t.Fatal("no budget records")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/docs/"+metas[0].ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	a.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("budget record via /docs = %d", rec.Code)
	}
	for _, id := range []string{"directory-log/head", "directory-log/e/000000000001"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/docs/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		a.ServeHTTP(rec, req)
		if rec.Code != 400 {
			t.Errorf("%s via /docs = %d", id, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/docs", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	a.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "ratelimit") || strings.Contains(rec.Body.String(), "directory") {
		t.Errorf("listing shows reserved ids: %s", rec.Body)
	}
}

func TestDirectoryLookupHasAGlobalBudget(t *testing.T) {
	e := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryLimits(func(name string, perMinute float64, burst int) docstore.Limiter {
			if strings.HasPrefix(name, "dir-lookup-all-") {
				return docstore.NewRateLimiter(1, 3) // every shard of the server-wide budget
			}
			return docstore.NewRateLimiter(perMinute, burst)
		})
	})
	var codes []int
	for _, u := range usersInShardOf(ana, 5, "x.io") { // five different users of one shard, one lookup each: its budget is 3
		code, _ := e.lookup(e.token(u), "bo@x.io")
		codes = append(codes, code)
	}
	if codes[2] != 200 || codes[3] != 429 || codes[4] != 429 {
		t.Errorf("%v", codes)
	}
	rec := e.do("GET", "/directory/lookup?email=bo@x.io", e.token(ana), "")
	if rec.Header().Get("Retry-After") == "" || rec.Code != 429 {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
	// The log reads draw on it too.
	if rec := e.do("GET", "/directory/log", e.token(ana), ""); rec.Code != 429 {
		t.Errorf("log = %d", rec.Code)
	}
}

func TestDirectoryLookupPolicy(t *testing.T) {
	policy, err := docstore.ParseDirectoryLookupMode("domain")
	if err != nil {
		t.Fatal(err)
	}
	e := newDirEnvWith(t, nil, func(s *docstore.Server) { s.WithDirectoryPolicy(policy) })
	anaTok := e.token(ana)
	e.publish(anaTok, map[string]string{"key": newRecovery(t).Text})
	carol := e.token(auth.User{ID: "google:3", Email: "carol@y.org", VerifiedEmail: true})
	e.publish(carol, map[string]string{"key": newRecovery(t).Text})

	if code, keys := e.lookup(e.token(bo), "ANA@x.io"); code != 200 || len(keys) != 1 {
		t.Errorf("same domain: %d %v", code, keys)
	}
	if code, _ := e.lookup(e.token(bo), "carol@y.org"); code != 403 {
		t.Errorf("another domain = %d", code)
	}
	// The refusal does not depend on whether the address has keys.
	if code, _ := e.lookup(e.token(bo), "nobody@y.org"); code != 403 {
		t.Errorf("another domain, no keys = %d", code)
	}
	if code, keys := e.lookup(e.token(bo), "nobody@x.io"); code != 200 || len(keys) != 0 {
		t.Errorf("same domain, no keys: %d %v", code, keys)
	}
	// An unverified address belongs to no domain.
	if code, _ := e.lookup(e.token(auth.User{ID: "google:9", Email: "z@x.io"}), "ana@x.io"); code != 403 {
		t.Errorf("unverified = %d", code)
	}
	// The log is not narrowed by it (it holds hashes of addresses and fingerprints).
	if rec := e.do("GET", "/directory/log", e.token(bo), ""); rec.Code != 200 {
		t.Errorf("log = %d", rec.Code)
	}
}

func TestDirectoryPolicies(t *testing.T) {
	u := auth.User{ID: "google:1", Email: "Ana@X.io", VerifiedEmail: true}
	contacts := docstore.Contacts(func(auth.User) []string { return []string{" Carol@Y.org "} })
	any := docstore.AnyOf(docstore.SameDomain(), contacts)
	for _, c := range []struct {
		name  string
		p     docstore.DirectoryPolicy
		email string
		want  bool
	}{
		{"all", docstore.AllowAllLookups, "q@z.com", true},
		{"same domain", docstore.SameDomain(), "bo@x.IO", true},
		{"other domain", docstore.SameDomain(), "bo@y.io", false},
		{"no domain", docstore.SameDomain(), "bo", false},
		{"subdomain is another", docstore.SameDomain(), "bo@mail.x.io", false},
		{"contact", contacts, "carol@y.org", true},
		{"not a contact", contacts, "dave@y.org", false},
		{"any of: domain", any, "bo@x.io", true},
		{"any of: contact", any, "CAROL@y.org", true},
		{"any of: neither", any, "dave@y.org", false},
		{"any of nothing", docstore.AnyOf(), "bo@x.io", false},
	} {
		if got := c.p.CanLookup(u, c.email); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if docstore.SameDomain().CanLookup(auth.User{Email: "ana@x.io"}, "bo@x.io") {
		t.Error("an unverified address has no domain")
	}
	for in, ok := range map[string]bool{"": true, "any": true, " Domain ": true, "contacts": false} {
		if _, err := docstore.ParseDirectoryLookupMode(in); (err == nil) != ok {
			t.Errorf("%q: %v", in, err)
		}
	}
}
