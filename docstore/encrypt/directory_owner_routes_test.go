package encrypt

import (
	"crypto/ed25519"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func decodeBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

// headPos asks the head route for the log position an owner signature names.
func (e *dirEnv) headPos(tok string) string {
	e.t.Helper()
	rec := e.do("GET", "/directory/log/head", tok, "")
	if rec.Code != 200 {
		e.t.Fatalf("head: %d %s", rec.Code, rec.Body)
	}
	h := decodeBody[docstore.DirectoryHead](e.t, rec.Body.Bytes())
	if err := VerifyHead(h, e.dir.Signer.(PublicKeyer).PublicKeys()); err != nil {
		e.t.Fatal(err)
	}
	return FormatLogPosition(h.Seq, h.Hash)
}

func (e *dirEnv) ownerSig(tok string, priv ed25519.PrivateKey, op, email, fp string) *docstore.DirectoryOwnerSig {
	if op == OpPublish { // version 2: covers the (empty) label
		s := SignOwnerPublish(priv, email, fp, "", time.Now(), e.headPos(tok))
		return &s
	}
	s := SignOwnerOp(priv, op, email, fp, time.Now(), e.headPos(tok))
	return &s
}

func TestOwnerRoutesEnrollPublishRevokeAndReset(t *testing.T) {
	e := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryLimits(func(_ string, perMinute float64, burst int) docstore.Limiter {
			return docstore.NewRateLimiter(perMinute*100, burst*100)
		})
	})
	tok := e.token(ana)
	_, priv := ownerKey(t)
	pub := priv.Public().(ed25519.PublicKey)

	if rec := e.do("GET", "/directory/enroll?email=ana@x.io", tok, ""); rec.Code != 200 || decodeBody[docstore.DirectoryEnrollment](t, rec.Body.Bytes()).Enrolled {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/directory/enroll?email=a@b@c", tok, ""); rec.Code != 400 {
		t.Errorf("malformed: %d", rec.Code)
	}
	// Enroll: challenge, then the request signed by the new key.
	rec := e.do("POST", "/directory/enroll/challenge", tok, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	ch := decodeBody[docstore.DirectoryEnrollChallenge](t, rec.Body.Bytes())
	req := docstore.DirectoryEnroll{Mode: OpEnroll, OwnerPub: FormatOwnerPub(pub), Token: ch.Token, Time: ch.Time, Prev: ch.Prev,
		Sig: SignOwnerOp(priv, OpEnroll, "ana@x.io", OwnerFingerprint(pub), time.Unix(ch.Time, 0), ch.Prev).Sig}
	if rec := e.post("/directory/enroll", tok, req); rec.Code != 200 || !decodeBody[docstore.DirectoryEnrollment](t, rec.Body.Bytes()).Enrolled {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.post("/directory/enroll", tok, req); rec.Code != 403 && rec.Code != 409 {
		t.Errorf("replayed enrollment: %d", rec.Code)
	}
	if rec := e.do("GET", "/directory/enroll?email=ana@x.io", e.token(bo), ""); rec.Code != 200 || !decodeBody[docstore.DirectoryEnrollment](t, rec.Body.Bytes()).Enrolled {
		t.Errorf("anybody who may look up the address sees that it is enrolled: %d %s", rec.Code, rec.Body)
	}

	// Publishing now needs the owner signature: 428 without, 403 with a wrong one, 200 with it.
	key := newRecovery(t)
	if rec := e.publish(tok, map[string]string{"key": key.Text}); rec.Code != 428 {
		t.Errorf("no owner signature: %d %s", rec.Code, rec.Body)
	}
	_, wrong := ownerKey(t)
	if rec := e.post("/directory/publish", tok, docstore.DirectoryPublish{Key: key.Text, Owner: e.ownerSig(tok, wrong, OpPublish, "ana@x.io", key.Fingerprint)}); rec.Code != 403 {
		t.Errorf("wrong owner signature: %d", rec.Code)
	}
	if rec := e.post("/directory/publish", tok, docstore.DirectoryPublish{Key: key.Text, Owner: e.ownerSig(tok, priv, OpPublish, "ana@x.io", key.Fingerprint)}); rec.Code != 200 {
		t.Fatalf("owner signature: %d %s", rec.Code, rec.Body)
	}
	code, keys := e.lookup(e.token(bo), "ana@x.io")
	if code != 200 || len(keys) != 1 || !keys[0].OwnerSigned || !keys[0].OwnerEnrolled || !VerifyOwnerEvidence("ana@x.io", keys[0]) {
		t.Fatalf("%d %+v", code, keys)
	}

	// Revoke carries the signature in the body.
	path := "/directory/keys/" + url.PathEscape(key.Fingerprint)
	if rec := e.do("DELETE", path, tok, ""); rec.Code != 428 {
		t.Errorf("revoke without: %d", rec.Code)
	}
	if rec := e.do("DELETE", path, tok, `{"owner": 5}`); rec.Code != 400 {
		t.Errorf("revoke with a body that is not one: %d", rec.Code)
	}
	body, _ := json.Marshal(docstore.DirectoryRevoke{Owner: e.ownerSig(tok, priv, OpRevoke, "ana@x.io", key.Fingerprint)})
	if rec := e.do("DELETE", path, tok, string(body)); rec.Code != 200 {
		t.Errorf("revoke with the owner signature: %d %s", rec.Code, rec.Body)
	}

	// Reset: the request is logged; cancelling needs the owner key; completing needs the wait.
	if rec := e.do("POST", "/directory/enroll/reset", tok, ""); rec.Code != 200 || decodeBody[docstore.DirectoryEnrollment](t, rec.Body.Bytes()).ResetSince == 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.post("/directory/enroll/cancel", tok, docstore.DirectoryRevoke{}); rec.Code != 428 {
		t.Errorf("cancel without the owner: %d", rec.Code)
	}
	if rec := e.post("/directory/enroll/cancel", tok, docstore.DirectoryRevoke{Owner: e.ownerSig(tok, priv, OpResetCancel, "ana@x.io", "")}); rec.Code != 200 || decodeBody[docstore.DirectoryEnrollment](t, rec.Body.Bytes()).ResetSince != 0 {
		t.Errorf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/directory/enroll/nothing", tok, ""); rec.Code != 404 {
		t.Errorf("unknown route: %d", rec.Code)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/directory/enroll/challenge"}, {"GET", "/directory/enroll/reset"}, {"PUT", "/directory/enroll"}, {"POST", "/directory/log/head"}, {"POST", "/directory/log/address"}} {
		if rec := e.do(c.method, c.path, tok, ""); rec.Code != 405 {
			t.Errorf("%s %s = %d", c.method, c.path, rec.Code)
		}
	}
	// None of the owner routes is open to a passkey session, nor to nobody.
	pk := e.token(auth.User{ID: "passkey:z", Email: "ana@x.io", VerifiedEmail: true})
	for _, c := range []struct{ method, path string }{{"GET", "/directory/enroll?email=ana@x.io"}, {"POST", "/directory/enroll/challenge"}, {"POST", "/directory/enroll"}, {"POST", "/directory/enroll/reset"}, {"POST", "/directory/enroll/cancel"}} {
		if rec := e.do(c.method, c.path, pk, "{}"); rec.Code != 403 {
			t.Errorf("passkey %s %s = %d", c.method, c.path, rec.Code)
		}
		if rec := e.do(c.method, c.path, "", "{}"); rec.Code != 401 {
			t.Errorf("signed out %s %s = %d", c.method, c.path, rec.Code)
		}
	}
	if rec := e.do("POST", "/directory/enroll", tok, "not json"); rec.Code != 400 {
		t.Errorf("bad body: %d", rec.Code)
	}
}

func TestEnrollmentLookupFollowsThePolicy(t *testing.T) {
	policy, _ := docstore.ParseDirectoryLookupMode("domain")
	e := newDirEnvWith(t, nil, func(s *docstore.Server) { s.WithDirectoryPolicy(policy) })
	tok := e.token(ana)
	if rec := e.do("GET", "/directory/enroll?email=carol@y.org", tok, ""); rec.Code != 403 {
		t.Errorf("another domain: %d", rec.Code)
	}
	if rec := e.do("GET", "/directory/enroll?email=bo@x.io", tok, ""); rec.Code != 200 {
		t.Errorf("same domain: %d", rec.Code)
	}
}
