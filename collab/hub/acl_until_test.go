package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// A grant whose end has passed opens nothing.
func TestACLRolesHonourTheEndOfAGrant(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	now := time.Now()
	acl := docstore.ACL{Owner: "o@gojiberry.io"}
	if err := acl.GrantFor("gone@gojiberry.io", docstore.RoleEditor, now.Add(-2*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := acl.GrantFor("here@gojiberry.io", docstore.RoleEditor, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(acl)
	_ = store.Save(context.Background(), "acl:doc", data)
	roles := ACLRoles(store, func(r *http.Request) string { return r.Header.Get("X-User") })
	as := func(user string) Role {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-User", user)
		return roles(req, "doc")
	}
	if got := as("gone@gojiberry.io"); got != "" {
		t.Errorf("an ended grant gives %q", got)
	}
	if got := as("here@gojiberry.io"); got != Editor {
		t.Errorf("a live grant gives %q", got)
	}
}
