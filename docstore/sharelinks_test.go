package docstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// linkFixture is a server with an owned document.
type linkFixture struct {
	t        *testing.T
	h        http.Handler
	owner    string // session token
	viewer   string
	stranger string
	clock    atomic.Int64 // unix seconds
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	provider := newFakeOIDCProvider(t)
	f := &linkFixture{t: t}
	f.clock.Store(time.Now().Unix())
	srv := newTestServerWithAuth(t, provider.srv.URL).WithClock(func() time.Time { return time.Unix(f.clock.Load(), 0) })
	f.h = srv.Handler()
	f.owner = sessionTokenFor(t, f.h, provider, "owner@example.com", "owner-sub")
	f.viewer = sessionTokenFor(t, f.h, provider, "viewer@example.com", "viewer-sub")
	f.stranger = sessionTokenFor(t, f.h, provider, "stranger@example.com", "stranger-sub")
	if rec := f.do(http.MethodPut, "/docs/doc1", "v1", f.owner, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("owner PUT %d", rec.Code)
	}
	if rec := f.do(http.MethodPut, "/docs/doc1/acl", `{"grants":{"viewer@example.com":"viewer"}}`, f.owner, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT acl %d", rec.Code)
	}
	return f
}

func (f *linkFixture) advance(d time.Duration) { f.clock.Add(int64(d / time.Second)) }

// do sends a request as a session.
func (f *linkFixture) do(method, path, body, session, share string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if session != "" {
		withBearer(req, session)
	}
	if share != "" {
		req.Header.Set(ShareHeader, share)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *linkFixture) newLink(role string, hours float64) (token, id string, expires time.Time) {
	f.t.Helper()
	b, _ := json.Marshal(map[string]any{"role": role, "hours": hours})
	rec := f.do(http.MethodPost, "/docs/doc1/acl", string(b), f.owner, "")
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create link: %d %s", rec.Code, rec.Body)
	}
	var out createLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out.Token, out.ID, out.Expires
}

func (f *linkFixture) want(code int, rec *httptest.ResponseRecorder, what string) {
	f.t.Helper()
	if rec.Code != code {
		f.t.Errorf("%s: status %d, want %d (%s)", what, rec.Code, code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestViewLinkReadsButCannotWriteOrDeleteOrManage(t *testing.T) {
	f := newLinkFixture(t)
	tok, _, _ := f.newLink("viewer", 24)

	f.want(http.StatusUnauthorized, f.do(http.MethodGet, "/docs/doc1", "", "", ""), "anonymous without the link")
	f.want(http.StatusForbidden, f.do(http.MethodGet, "/docs/doc1", "", f.stranger, ""), "a stranger without the link")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "anonymous with the view link")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", f.stranger, tok), "a stranger with the view link")
	f.want(http.StatusUnauthorized, f.do(http.MethodPut, "/docs/doc1", "hacked", "", tok), "view link cannot write")
	f.want(http.StatusForbidden, f.do(http.MethodPut, "/docs/doc1", "hacked", f.stranger, tok), "view link cannot write, signed in")
	f.want(http.StatusUnauthorized, f.do(http.MethodPost, "/docs/doc1/renew", "", "", tok), "view link cannot renew")
	f.want(http.StatusUnauthorized, f.do(http.MethodDelete, "/docs/doc1", "", "", tok), "view link cannot delete")
	f.want(http.StatusForbidden, f.do(http.MethodGet, "/docs/doc1/acl", "", "", tok), "link cannot read the access list")
	f.want(http.StatusForbidden, f.do(http.MethodGet, "/docs/doc1/acl", "", f.stranger, tok), "link cannot read the access list, signed in")
	f.want(http.StatusForbidden, f.do(http.MethodPut, "/docs/doc1/acl", `{"grants":{"stranger@example.com":"editor"}}`, f.stranger, tok), "link cannot replace the access list")
	f.want(http.StatusForbidden, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"editor","hours":1}`, f.stranger, tok), "link cannot make links")
	if got := f.do(http.MethodGet, "/docs/doc1", "", f.owner, ""); got.Body.String() != "v1" {
		t.Fatalf("the document changed: %q", got.Body)
	}
}

func TestEditLinkWritesButCannotDeleteOrManage(t *testing.T) {
	f := newLinkFixture(t)
	tok, _, _ := f.newLink("editor", 1)
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc1", "v2", "", tok), "edit link writes")
	if got := f.do(http.MethodGet, "/docs/doc1", "", f.owner, ""); got.Body.String() != "v2" {
		t.Fatalf("the edit did not land: %q", got.Body)
	}
	f.want(http.StatusUnauthorized, f.do(http.MethodDelete, "/docs/doc1", "", "", tok), "edit link cannot delete")
	f.want(http.StatusForbidden, f.do(http.MethodPut, "/docs/doc1/acl", `{}`, f.stranger, tok), "edit link cannot replace the access list")
	f.want(http.StatusUnauthorized, f.do(http.MethodPut, "/docs/doc1", "x", "", tok[:len(tok)-1]), "a wrong token")
	f.want(http.StatusUnauthorized, f.do(http.MethodPut, "/docs/doc1", "x", "", "short"), "a short token")
}

func TestExpiredLinkIsRefusedAtTheDeadline(t *testing.T) {
	f := newLinkFixture(t)
	tok, _, expires := f.newLink("viewer", 2)
	if want := time.Unix(f.clock.Load(), 0).Add(2 * time.Hour); !expires.Equal(want) {
		t.Fatalf("expires %v, want %v", expires, want)
	}
	f.advance(2*time.Hour - time.Second)
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "one second before the deadline")
	f.advance(time.Second)
	f.want(http.StatusUnauthorized, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "at the deadline")
	f.advance(24 * time.Hour)
	f.want(http.StatusUnauthorized, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "long after")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", f.viewer, tok), "the named viewer with an expired link")
}

func TestExpiredEditLinkCannotWrite(t *testing.T) {
	f := newLinkFixture(t)
	tok, _, _ := f.newLink("editor", 1)
	f.advance(time.Hour)
	f.want(http.StatusUnauthorized, f.do(http.MethodPut, "/docs/doc1", "late", "", tok), "expired edit link")
	if got := f.do(http.MethodGet, "/docs/doc1", "", f.owner, ""); got.Body.String() != "v1" {
		t.Fatalf("an expired link wrote: %q", got.Body)
	}
}

func TestRevokedLinkIsRefusedAtOnceAndOthersKeepWorking(t *testing.T) {
	f := newLinkFixture(t)
	tok, id, _ := f.newLink("viewer", 24)
	other, _, _ := f.newLink("viewer", 24)
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "before revoking")

	f.want(http.StatusForbidden, f.do(http.MethodDelete, "/docs/doc1/acl?link="+id, "", f.viewer, ""), "a named viewer cannot revoke")
	f.want(http.StatusForbidden, f.do(http.MethodDelete, "/docs/doc1/acl?link="+id, "", f.stranger, tok), "the link holder cannot revoke")
	f.want(http.StatusUnauthorized, f.do(http.MethodDelete, "/docs/doc1/acl?link="+id, "", "", ""), "anonymous cannot revoke")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "still works after refused revokes")

	f.want(http.StatusNoContent, f.do(http.MethodDelete, "/docs/doc1/acl?link="+id, "", f.owner, ""), "the owner revokes")
	f.want(http.StatusUnauthorized, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "after revoking")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", other), "another link is unaffected")
	f.want(http.StatusNotFound, f.do(http.MethodDelete, "/docs/doc1/acl?link="+id, "", f.owner, ""), "revoking twice")
	f.want(http.StatusBadRequest, f.do(http.MethodDelete, "/docs/doc1/acl", "", f.owner, ""), "no link named")
}

func TestOnlyTheOwnerMakesLinksAndTheTokenIsNeverStored(t *testing.T) {
	f := newLinkFixture(t)
	f.want(http.StatusForbidden, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer","hours":1}`, f.viewer, ""), "a named viewer")
	f.want(http.StatusForbidden, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer","hours":1}`, f.stranger, ""), "a stranger")
	f.want(http.StatusUnauthorized, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer","hours":1}`, "", ""), "anonymous")

	tok, id, _ := f.newLink("viewer", 5)
	if id == tok || id != LinkID(tok) {
		t.Fatalf("the list names a link by a hash of its token: %q", id)
	}
	rec := f.do(http.MethodGet, "/docs/doc1/acl", "", f.owner, "")
	if strings.Contains(rec.Body.String(), tok) {
		t.Fatalf("the access list contains the token: %s", rec.Body)
	}
	var acl ACL
	if err := json.Unmarshal(rec.Body.Bytes(), &acl); err != nil {
		t.Fatal(err)
	}
	if l, ok := acl.Links[id]; !ok || l.Role != RoleViewer || l.Expires.Sub(l.Created) != 5*time.Hour {
		t.Fatalf("the owner's list: %+v", acl.Links)
	}
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc1/acl", `{"grants":{},"links":{"`+LinkID("forged-token-forged-token-forged-xx")+`":{"role":"editor","expires":"2999-01-01T00:00:00Z"}}}`, f.owner, ""), "owner replaces the list")
	f.want(http.StatusUnauthorized, f.do(http.MethodPut, "/docs/doc1", "x", "", "forged-token-forged-token-forged-xx"), "a link smuggled in through PUT")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", "", tok), "the made link survives a PUT of the list")
}

func TestLinkRequestsAreValidated(t *testing.T) {
	f := newLinkFixture(t)
	for _, body := range []string{
		`{"role":"owner","hours":1}`, `{"role":"commenter","hours":1}`, `{"role":"","hours":1}`,
		`{"role":"viewer","hours":-1}`, `{"role":"viewer","hours":100000}`, `{"role":"viewer","hours":0.0001}`,
		`not json`,
	} {
		f.want(http.StatusBadRequest, f.do(http.MethodPost, "/docs/doc1/acl", body, f.owner, ""), body)
	}
	rec := f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer"}`, f.owner, "")
	var out createLinkResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated || out.Expires.Sub(time.Unix(f.clock.Load(), 0)) != DefaultLinkLife {
		t.Fatalf("default life: %d %+v", rec.Code, out)
	}
	for range MaxLinks - 1 {
		f.newLink("viewer", 1)
	}
	f.want(http.StatusBadRequest, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer","hours":1}`, f.owner, ""), "too many links")
	f.advance(2 * time.Hour) // the one-hour links lapse, which frees room
	f.want(http.StatusCreated, f.do(http.MethodPost, "/docs/doc1/acl", `{"role":"viewer","hours":1}`, f.owner, ""), "room after expiry")
}

func TestLinksDoNotOpenADocumentThatHasNoAccessList(t *testing.T) {
	f := newLinkFixture(t)
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc2", "x", "", ""), "an unshared document")
	tok, _, _ := f.newLink("viewer", 1)
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc3/acl", `{}`, f.stranger, ""), "stranger shares doc3")
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc3", "private", f.stranger, ""), "stranger writes doc3")
	f.want(http.StatusUnauthorized, f.do(http.MethodGet, "/docs/doc3", "", "", tok), "doc1's link on doc3")
}

func TestWhoCanSeeListsOwnerPeopleAndLiveLinks(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	a := ACL{Owner: "Owner@Example.com"}
	if err := a.Grant("ana@example.com", RoleEditor); err != nil {
		t.Fatal(err)
	}
	tokA, tokB := strings.Repeat("a", 43), strings.Repeat("b", 43)
	idA, _, err := a.AddLink(tokA, RoleViewer, now, 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	idB, _, _ := a.AddLink(tokB, RoleEditor, now, time.Hour)
	got := a.WhoCanSee(now)
	if len(got) != 4 || got[0].Kind != "owner" || got[1].Kind != "person" || got[1].Role != "editor" ||
		got[2].ID != idB || got[2].Kind != "link" || got[3].ID != idA || !got[3].Expires.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("%+v", got)
	}
	if got := a.WhoCanSee(now.Add(2 * time.Hour)); len(got) != 3 || got[2].ID != idA {
		t.Fatalf("an expired link is still listed: %+v", got)
	}
	if !a.RevokeLink(idA) || a.RevokeLink(idA) || len(a.WhoCanSee(now)) != 3 {
		t.Fatal("revoke")
	}
	if a.LinkRole(tokA, now) != "" || a.LinkRole(tokB, now) != RoleEditor || a.LinkRole("", now) != "" {
		t.Fatal("link roles")
	}
	if _, _, err := a.AddLink("short", RoleViewer, now, time.Hour); err == nil {
		t.Fatal("a short token was accepted")
	}
}
