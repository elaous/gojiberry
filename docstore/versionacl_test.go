package docstore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The history of a shared document follows the document's access list: a viewer reads it and writes nothing, an
// editor adds and deletes versions, a stranger does nothing, and a history of an unshared document is as before.
func TestVersionHistoryFollowsTheDocumentsAccessList(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL).WithRequireACL(true)
	h := srv.Handler()
	tok := map[string]string{}
	for _, who := range []string{"owner", "editor", "viewer", "stranger"} {
		tok[who] = sessionTokenFor(t, h, provider, who+"@example.com", who+"-sub")
	}
	do := func(who, method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if who != "" {
			req = withBearer(req, tok[who])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	// the owner saves "plan.open" (the saver's id is "plan") and shares it
	if do("owner", "PUT", "/docs/plan.open", "v1") != http.StatusNoContent {
		t.Fatal("save")
	}
	if do("owner", "PUT", "/docs/plan.open/acl", `{"grants":{"editor@example.com":"editor","viewer@example.com":"viewer"}}`) != http.StatusNoContent {
		t.Fatal("share")
	}
	v := "/docs/versions/plan/00000000000000000001.open"
	meta := "/docs/versions/plan/meta.json"
	for _, c := range []struct {
		who, method, path string
		want              int
	}{
		{"owner", "PUT", v, http.StatusNoContent},
		{"editor", "PUT", "/docs/versions/plan/00000000000000000002.open", http.StatusNoContent},
		{"editor", "PUT", meta, http.StatusNoContent},
		{"viewer", "GET", v, http.StatusOK},
		{"viewer", "GET", meta, http.StatusOK},
		{"viewer", "PUT", "/docs/versions/plan/00000000000000000003.open", http.StatusForbidden},
		{"viewer", "PUT", meta, http.StatusForbidden},
		{"viewer", "DELETE", v, http.StatusForbidden},
		{"stranger", "GET", v, http.StatusForbidden},
		{"stranger", "PUT", "/docs/versions/plan/00000000000000000004.open", http.StatusForbidden},
		{"", "GET", v, http.StatusUnauthorized},
		{"editor", "DELETE", "/docs/versions/plan/00000000000000000002.open", http.StatusNoContent},
		{"owner", "DELETE", v, http.StatusNoContent},
	} {
		if got := do(c.who, c.method, c.path, "snapshot"); got != c.want {
			t.Errorf("%s %s %s = %d, want %d", c.who, c.method, c.path, got, c.want)
		}
	}
	// A history whose document has no access list is decided as an unshared key is (closed to a stranger here only
	// because the first writer would own it): nothing changes for it. The stranger becomes the owner of their key.
	if got := do("stranger", "PUT", "/docs/versions/nobody/1.open", "x"); got != http.StatusNoContent {
		t.Errorf("an unshared history: %d", got)
	}
	if got := do("owner", "GET", "/docs/versions/nobody/1.open", ""); got != http.StatusForbidden {
		t.Errorf("and it stays its owner's: %d", got)
	}
	// Keys that only look like histories are not.
	for _, k := range []string{"versions/plan", "versions//x", "versions/plan/a/b", "versions/plan/.."} {
		if _, ok := versionDocID(k); ok {
			t.Errorf("%q is not a version key", k)
		}
	}
}
