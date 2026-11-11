package docstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
)

type stubDirectory struct {
	DirectoryService
	err            error
	keys           []DirectoryKey
	published      DirectoryPublish
	revoked        string
	revokedOwner   *DirectoryOwnerSig
	includeRevoked bool
	asked          string
	enrollment     DirectoryEnrollment
	log            DirectoryLog
	logAddress     DirectoryLogAddress
	head           DirectoryHead
	filteredFor    []string
	resetPseudonym string
	unmapped       DirectoryUnmapped
	cancelOwner    *DirectoryOwnerSig
	enrolled       DirectoryEnroll
}

func (d *stubDirectory) Enrollment(_ context.Context, email string) (DirectoryEnrollment, error) {
	d.asked = email
	return d.enrollment, d.err
}

func (d *stubDirectory) EnrollChallenge(context.Context, auth.User) (DirectoryEnrollChallenge, error) {
	return DirectoryEnrollChallenge{}, d.err
}

func (d *stubDirectory) Enroll(_ context.Context, _ auth.User, req DirectoryEnroll) (DirectoryEnrollment, error) {
	d.enrolled = req
	return d.enrollment, d.err
}

func (d *stubDirectory) RequestReset(context.Context, auth.User) (DirectoryEnrollment, error) {
	return d.enrollment, d.err
}

func (d *stubDirectory) CancelReset(_ context.Context, _ auth.User, owner *DirectoryOwnerSig) (DirectoryEnrollment, error) {
	d.cancelOwner = owner
	return d.enrollment, d.err
}

func (d *stubDirectory) Log(context.Context, int64, int) (DirectoryLog, error) { return d.log, d.err }
func (d *stubDirectory) Head(context.Context) (DirectoryHead, error)           { return d.head, d.err }
func (d *stubDirectory) LogAddress(context.Context, auth.User) (DirectoryLogAddress, error) {
	return d.logAddress, d.err
}

type filteringDirectory struct{ *stubDirectory }

func (d filteringDirectory) LogFor(_ context.Context, addresses []string, _ int64, _, _ int) (DirectoryLog, error) {
	d.filteredFor = addresses
	return DirectoryLog{Entries: nil}, d.err
}

type operatorDirectory struct{ *stubDirectory }

func (d operatorDirectory) OperatorResetEnrollment(_ context.Context, pseudonym string) (DirectoryEnrollment, error) {
	d.resetPseudonym = pseudonym
	return DirectoryEnrollment{}, d.err
}

func (d operatorDirectory) UnmappedAddresses(context.Context) (DirectoryUnmapped, error) {
	return d.unmapped, d.err
}

func (d *stubDirectory) Challenge(_ context.Context, _ auth.User, key string) (DirectoryChallenge, error) {
	d.asked = key
	return DirectoryChallenge{}, d.err
}

func (d *stubDirectory) Publish(_ context.Context, _ auth.User, req DirectoryPublish) (DirectoryKey, error) {
	d.published = req
	return DirectoryKey{Fingerprint: "SHA256:new"}, d.err
}

func (d *stubDirectory) Revoke(_ context.Context, _ auth.User, fp string, owner *DirectoryOwnerSig) (DirectoryKey, error) {
	d.revoked, d.revokedOwner = fp, owner
	return DirectoryKey{Fingerprint: fp}, d.err
}

func (d *stubDirectory) Lookup(_ context.Context, email string, includeRevoked bool) ([]DirectoryKey, error) {
	d.asked, d.includeRevoked = email, includeRevoked
	return d.keys, d.err
}

type dirRoutes struct {
	t   *testing.T
	h   http.Handler
	mgr *auth.Manager
	dir *stubDirectory
}

func newDirRoutes(t *testing.T, opt func(*Server)) *dirRoutes {
	t.Helper()
	return newDirRoutesWith(t, nil, opt)
}

func newDirRoutesWith(t *testing.T, wrap func(*stubDirectory) DirectoryService, opt func(*Server)) *dirRoutes {
	t.Helper()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	dir := &stubDirectory{}
	var svc DirectoryService = dir
	if wrap != nil {
		svc = wrap(dir)
	}
	srv := NewServer(NewMemoryAdapter(), "memory").WithAuth(mgr).WithDirectory(svc)
	if opt != nil {
		opt(srv)
	}
	return &dirRoutes{t: t, h: srv.Handler(), mgr: mgr, dir: dir}
}

func (d *dirRoutes) do(method, path string, u *auth.User, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4000"
	if u != nil {
		tok, err := d.mgr.StartSession(*u)
		if err != nil {
			d.t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	d.h.ServeHTTP(rec, req)
	return rec
}

var (
	ana     = auth.User{ID: "google:ana", Email: "Ana@Corp.example", VerifiedEmail: true}
	passkey = auth.User{ID: "passkey:1", Email: "ana@corp.example", VerifiedEmail: true}
)

func TestDirectoryRoutesNeedTheRightSession(t *testing.T) {
	d := newDirRoutes(t, nil)
	for _, rt := range []struct{ method, path, body string }{
		{"POST", "/directory/challenge", `{"key":"k"}`}, {"POST", "/directory/publish", `{"key":"k"}`},
		{"DELETE", "/directory/keys/SHA256-x", ""}, {"GET", "/directory/lookup?email=bo@corp.example", ""},
	} {
		if rec := d.do(rt.method, rt.path, nil, rt.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s signed out: %d", rt.method, rt.path, rec.Code)
		}
	}
	for _, rt := range []struct{ method, path, body string }{
		{"POST", "/directory/challenge", `{"key":"k"}`}, {"POST", "/directory/publish", `{"key":"k"}`},
		{"DELETE", "/directory/keys/SHA256-x", ""}, {"GET", "/directory/lookup?email=bo@corp.example", ""},
	} {
		if rec := d.do(rt.method, rt.path, &passkey, rt.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a passkey account: %d", rt.method, rt.path, rec.Code)
		}
	}
	unverified := auth.User{ID: "google:x", Email: "x@corp.example"}
	if rec := d.do("POST", "/directory/publish", &unverified, `{"key":"k"}`); rec.Code != http.StatusForbidden {
		t.Errorf("an unverified e-mail cannot vouch for an address: %d", rec.Code)
	}
}

func TestDirectoryRoutesRefuseTheWrongMethod(t *testing.T) {
	d := newDirRoutes(t, nil)
	for _, rt := range []struct{ method, path string }{
		{"GET", "/directory/challenge"}, {"GET", "/directory/publish"}, {"POST", "/directory/keys/SHA256-x"}, {"POST", "/directory/lookup?email=a@b.example"},
	} {
		if rec := d.do(rt.method, rt.path, &ana, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestDirectoryChallengeAndPublishRoutes(t *testing.T) {
	d := newDirRoutes(t, nil)
	if rec := d.do("POST", "/directory/challenge", &ana, `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("a body that is not JSON: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/challenge", &ana, `{"key":"  "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no key: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/challenge", &ana, `{"key":" ssh-ed25519 AAAA "}`); rec.Code != http.StatusOK || d.dir.asked != "ssh-ed25519 AAAA" {
		t.Errorf("challenge: %d, asked %q", rec.Code, d.dir.asked)
	}
	if rec := d.do("POST", "/directory/publish", &ana, `{"key":""}`); rec.Code != http.StatusBadRequest {
		t.Errorf("publish with no key: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/publish", &ana, `{"key":"k","email":"bo@corp.example"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a key for somebody else's address: %d", rec.Code)
	}
	rec := d.do("POST", "/directory/publish", &ana, `{"key":" k ","email":"ANA@corp.example"}`)
	if rec.Code != http.StatusOK || d.dir.published.Key != "k" || !strings.Contains(rec.Body.String(), "SHA256:new") {
		t.Errorf("publish for the own address: %d %s (%+v)", rec.Code, rec.Body.String(), d.dir.published)
	}
	big := `{"key":"` + strings.Repeat("k", maxDirectoryBody) + `"}`
	if rec := d.do("POST", "/directory/publish", &ana, big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over the limit: %d", rec.Code)
	}
}

func TestDirectoryErrorsBecomeStatuses(t *testing.T) {
	for err, want := range map[error]int{
		ErrDirectoryRefused: 403, ErrDirectoryBadKey: 400, ErrDirectoryBadAddress: 400, ErrDirectoryProofRequired: 428,
		ErrDirectoryEnrollmentRequired: 428, ErrDirectoryBadProof: 403, ErrDirectoryBadAttestation: 400, ErrDirectoryOwnerRequired: 428,
		ErrDirectoryBadOwnerSig: 403, ErrDirectoryEnrolled: 409, ErrDirectoryNotEnrolled: 409, ErrDirectoryResetWait: 409,
		ErrDirectoryNotFound: 404, ErrDirectoryRevoked: 409, ErrDirectoryBusy: 503, errors.New("disk on fire"): 500,
	} {
		d := newDirRoutes(t, nil)
		d.dir.err = err
		rec := d.do("POST", "/directory/publish", &ana, `{"key":"k"}`)
		if rec.Code != want {
			t.Errorf("%v: %d, want %d", err, rec.Code, want)
		}
		if want == 503 && rec.Header().Get("Retry-After") == "" {
			t.Error("a busy directory says when to come back")
		}
		if want == 500 && strings.Contains(rec.Body.String(), "disk on fire") {
			t.Error("an internal error is not shown to the caller")
		}
	}
}

func TestDirectoryRevokeRoute(t *testing.T) {
	d := newDirRoutes(t, nil)
	if rec := d.do("DELETE", "/directory/keys/", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no fingerprint: %d", rec.Code)
	}
	if rec := d.do("DELETE", "/directory/keys/"+strings.Repeat("a", 201), &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("a fingerprint that is too long: %d", rec.Code)
	}
	if rec := d.do("DELETE", "/directory/keys/SHA256-abc", &ana, ""); rec.Code != http.StatusOK || d.dir.revoked != "SHA256-abc" || d.dir.revokedOwner != nil {
		t.Errorf("revoke with no body: %d (%q, %v)", rec.Code, d.dir.revoked, d.dir.revokedOwner)
	}
	if rec := d.do("DELETE", "/directory/keys/SHA256-abc", &ana, `{"owner":{}}`); rec.Code != http.StatusOK || d.dir.revokedOwner == nil {
		t.Errorf("revoke with the owner's signature: %d", rec.Code)
	}
	if rec := d.do("DELETE", "/directory/keys/SHA256-abc", &ana, `{oops`); rec.Code != http.StatusBadRequest {
		t.Errorf("a broken body: %d", rec.Code)
	}
	d.dir.err = ErrDirectoryOwnerRequired
	if rec := d.do("DELETE", "/directory/keys/SHA256-abc", &ana, ""); rec.Code != http.StatusPreconditionRequired {
		t.Errorf("an address with an owner key: %d", rec.Code)
	}
}

func TestDirectoryLookupRoute(t *testing.T) {
	d := newDirRoutes(t, nil)
	if rec := d.do("GET", "/directory/lookup", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no address: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/lookup?email=a@b@c", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("not an address: %d", rec.Code)
	}
	rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"keys":[]`) {
		t.Errorf("no keys is an empty list, not null: %d %s", rec.Code, rec.Body.String())
	}
	d.dir.keys = make([]DirectoryKey, maxLookupKeys+10)
	rec = d.do("GET", "/directory/lookup?email=bo@corp.example&revoked=1", &ana, "")
	if rec.Code != 200 || !d.dir.includeRevoked || strings.Count(rec.Body.String(), `"fingerprint"`) > maxLookupKeys {
		t.Errorf("revoked=1 and the cap: %d includeRevoked=%v", rec.Code, d.dir.includeRevoked)
	}
	d.dir.err = errors.New("store down")
	if rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("a failing service: %d", rec.Code)
	}
}

func TestDirectoryLookupPolicyAndBudgets(t *testing.T) {
	d := newDirRoutes(t, func(s *Server) { s.WithDirectoryPolicy(SameDomain()) })
	if rec := d.do("GET", "/directory/lookup?email=bo@other.example", &ana, ""); rec.Code != http.StatusForbidden {
		t.Errorf("another domain under SameDomain: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, ""); rec.Code != http.StatusOK {
		t.Errorf("the own domain: %d", rec.Code)
	}
	limited := 0
	for i := 0; i < lookupsPerMinute+5; i++ {
		rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, "")
		if rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	if limited == 0 {
		t.Error("the lookup budget never ran out")
	}
	d = newDirRoutes(t, nil)
	hit := false
	for i := 0; i < publishesPerMinute+5; i++ {
		if d.do("POST", "/directory/publish", &ana, `{"key":"k"}`).Code == http.StatusTooManyRequests {
			hit = true
		}
	}
	if !hit {
		t.Error("the publish budget never ran out")
	}
}

func TestDirectoryClientIPOption(t *testing.T) {
	var asked bool
	d := newDirRoutes(t, func(s *Server) {
		s.WithDirectoryClientIP(func(r *http.Request) string { asked = true; return "198.51.100.1" })
	})
	if rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, ""); rec.Code != 200 || !asked {
		t.Errorf("the configured client address is used: %d %v", rec.Code, asked)
	}
}

func TestDirectoryGlobalPerDomainAndLimitsOptions(t *testing.T) {
	d := newDirRoutes(t, func(s *Server) {
		s.WithDirectoryGlobalPerDomain(true).WithDirectoryLimits(MemoryLimiters())
	})
	if rec := d.do("GET", "/directory/lookup?email=bo@corp.example", &ana, ""); rec.Code != 200 {
		t.Errorf("lookup with a budget per domain: %d", rec.Code)
	}
}

func TestDirectoryEnrollmentRoutes(t *testing.T) {
	d := newDirRoutes(t, nil)
	d.dir.enrollment = DirectoryEnrollment{Enrolled: true, Seq: 3}
	if rec := d.do("GET", "/directory/enroll?email=nope", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("not an address: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/enroll?email=bo@corp.example", &ana, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enrolled":true`) {
		t.Errorf("read: %d %s", rec.Code, rec.Body.String())
	}
	if rec := d.do("GET", "/directory/enroll?email=bo@corp.example", &passkey, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a passkey account does not read: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll/challenge", &ana, ""); rec.Code != 200 {
		t.Errorf("challenge: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll", &ana, `{"first":"x"}`); rec.Code != 200 {
		t.Errorf("enroll: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll", &ana, `{{`); rec.Code != http.StatusBadRequest {
		t.Errorf("enroll with a broken body: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll/reset", &ana, ""); rec.Code != 200 {
		t.Errorf("reset: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll/cancel", &ana, `{"owner":{}}`); rec.Code != 200 || d.dir.cancelOwner == nil {
		t.Errorf("cancel: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll/cancel", &ana, `x`); rec.Code != http.StatusBadRequest {
		t.Errorf("cancel with a broken body: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/enroll/other", &ana, ""); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown sub-route: %d", rec.Code)
	}
	for _, rt := range []struct{ method, path string }{
		{"GET", "/directory/enroll/challenge"}, {"GET", "/directory/enroll/reset"}, {"GET", "/directory/enroll/cancel"}, {"PUT", "/directory/enroll"},
	} {
		if rec := d.do(rt.method, rt.path, &ana, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", rt.method, rt.path, rec.Code)
		}
	}
	d.dir.err = ErrDirectoryEnrolled
	if rec := d.do("POST", "/directory/enroll", &ana, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("an error from the service: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/enroll?email=bo@corp.example", &ana, ""); rec.Code != http.StatusConflict {
		t.Errorf("a failing read: %d", rec.Code)
	}
}

func TestDirectoryLogRoutes(t *testing.T) {
	d := newDirRoutes(t, nil)
	if rec := d.do("GET", "/directory/log?since=abc", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("a since that is not a number: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/log?since=-1", &ana, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("a negative since: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/log", &ana, ""); rec.Code != http.StatusForbidden {
		t.Errorf("not enrolled, no filter: %d", rec.Code)
	}
	d.dir.enrollment = DirectoryEnrollment{Enrolled: true}
	d.dir.log = DirectoryLog{Entries: make([]DirectoryLogEntry, maxLogEntries+5)}
	rec := d.do("GET", "/directory/log?since=2", &ana, "")
	if rec.Code != 200 || strings.Count(rec.Body.String(), `"seq"`) > maxLogEntries+1 {
		t.Errorf("an owner of an enrolled address reads the whole log, capped: %d", rec.Code)
	}
	d.dir.log = DirectoryLog{}
	if rec := d.do("GET", "/directory/log", &ana, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("an empty log is a list: %d %s", rec.Code, rec.Body.String())
	}
	d.dir.err = ErrDirectoryBusy
	if rec := d.do("GET", "/directory/log", &ana, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a busy directory: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/log", &ana, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}

	op := newDirRoutes(t, func(s *Server) {
		s.WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" })
	})
	req := httptest.NewRequest("GET", "/directory/log", nil)
	tok, _ := op.mgr.StartSession(ana)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Admin", "yes")
	rec = httptest.NewRecorder()
	op.h.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"filtered"`) {
		t.Errorf("the operator: %d %s", rec.Code, rec.Body.String())
	}

	f := newDirRoutesWith(t, func(s *stubDirectory) DirectoryService { return filteringDirectory{s} }, nil)
	f.dir.logAddress = DirectoryLogAddress{Address: "p1", Previous: []string{"p0"}}
	if rec := f.do("GET", "/directory/log", &ana, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"filtered":true`) || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("filtered: %d %s", rec.Code, rec.Body.String())
	}
	f.dir.err = ErrDirectoryRefused
	if rec := f.do("GET", "/directory/log", &ana, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a directory that refuses the address: %d", rec.Code)
	}
	f.dir.err = ErrDirectoryBusy
	if rec := f.do("GET", "/directory/log", &ana, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failing address lookup: %d", rec.Code)
	}
}

func TestDirectoryLogPolicyAndHeadAndAddress(t *testing.T) {
	d := newDirRoutes(t, func(s *Server) {
		s.WithDirectoryPolicy(DirectoryPolicyFunc(func(auth.User, string) bool { return false }))
	})
	if rec := d.do("GET", "/directory/log/head", &ana, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a policy that refuses the log: %d", rec.Code)
	}
	d = newDirRoutes(t, nil)
	d.dir.head = DirectoryHead{Seq: 9}
	if rec := d.do("GET", "/directory/log/head", &ana, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "9") {
		t.Errorf("head: %d %s", rec.Code, rec.Body.String())
	}
	if rec := d.do("POST", "/directory/log/head", &ana, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST head: %d", rec.Code)
	}
	d.dir.logAddress = DirectoryLogAddress{Address: "abc"}
	if rec := d.do("GET", "/directory/log/address", &ana, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "abc") {
		t.Errorf("address: %d %s", rec.Code, rec.Body.String())
	}
	if rec := d.do("GET", "/directory/log/address", &passkey, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a passkey account has no log address: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/log/address", &ana, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST address: %d", rec.Code)
	}
	d.dir.err = ErrDirectoryBusy
	if rec := d.do("GET", "/directory/log/head", &ana, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("head of a busy directory: %d", rec.Code)
	}
	if rec := d.do("GET", "/directory/log/address", &ana, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("address of a busy directory: %d", rec.Code)
	}
}

func TestDirectoryOperatorRoutes(t *testing.T) {
	admin := func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" }
	d := newDirRoutesWith(t, func(s *stubDirectory) DirectoryService { return operatorDirectory{s} }, func(s *Server) { s.WithDirectoryAdmin(admin) })
	call := func(method, path string, isAdmin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "203.0.113.9:4000"
		if isAdmin {
			req.Header.Set("X-Admin", "yes")
		}
		rec := httptest.NewRecorder()
		d.h.ServeHTTP(rec, req)
		return rec
	}
	pseudonym := strings.Repeat("ab", 32)
	reset := "/admin/directory/reset-enrollment/" + pseudonym
	if rec := call("GET", reset, true); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET reset: %d", rec.Code)
	}
	if rec := call("POST", "/admin/directory/reset-enrollment/short", true); rec.Code != http.StatusBadRequest {
		t.Errorf("a pseudonym that is not 64 hex characters: %d", rec.Code)
	}
	if rec := call("POST", "/admin/directory/reset-enrollment/"+strings.Repeat("AB", 32), true); rec.Code != http.StatusBadRequest {
		t.Errorf("upper case is not the form: %d", rec.Code)
	}
	if rec := call("POST", reset, true); rec.Code != 200 || d.dir.resetPseudonym != pseudonym {
		t.Errorf("reset: %d (%q)", rec.Code, d.dir.resetPseudonym)
	}
	d.dir.err = ErrDirectoryNotEnrolled
	if rec := call("POST", reset, true); rec.Code != http.StatusConflict {
		t.Errorf("a failing reset: %d", rec.Code)
	}
	d.dir.err = nil
	if rec := call("GET", "/admin/directory/unmapped", true); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"addresses":[]`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("unmapped: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("POST", "/admin/directory/unmapped", true); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST unmapped: %d", rec.Code)
	}
	d.dir.err = ErrDirectoryBusy
	if rec := call("GET", "/admin/directory/unmapped", true); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failing unmapped read: %d", rec.Code)
	}
	denied, limited := 0, 0
	for i := 0; i < adminFailuresPerMinute+3; i++ {
		switch call("POST", reset, false).Code {
		case http.StatusForbidden:
			denied++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if denied != adminFailuresPerMinute || limited != 3 {
		t.Errorf("wrong credentials: %d refused, %d limited", denied, limited)
	}
	if rec := call("GET", "/admin/directory/unmapped", false); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the failure budget is shared by the admin routes: %d", rec.Code)
	}

	plain := newDirRoutes(t, func(s *Server) { s.WithDirectoryAdmin(admin) })
	for _, rt := range []struct{ method, path string }{{"POST", reset}, {"GET", "/admin/directory/unmapped"}} {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("X-Admin", "yes")
		rec := httptest.NewRecorder()
		plain.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s on a service without it: %d", rt.method, rt.path, rec.Code)
		}
	}
}
