package hub

import (
	"encoding/json"
	"net/http"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// ACLRoles is a RoleFunc that reads the access list docstore keeps for each
// document (the "acl:<id>" record that docstore's PUT /docs/{id}/acl writes):
// the owner, and who is an editor or a viewer. A document with no list is
// open to everyone as an owner, which is docstore's own rule. who names the
// caller (see AuthUser); a nil who, or an unknown caller, is anonymous and
// gets no role on a document that has a list.
func ACLRoles(store docstore.Adapter, who func(*http.Request) string) RoleFunc {
	return func(r *http.Request, id string) Role {
		stored, err := store.Load(r.Context(), "acl:"+id)
		if err != nil {
			return Owner // none stored (or unreadable): the document is open
		}
		var acl docstore.ACL
		if json.Unmarshal(stored.Data, &acl) != nil {
			return "" // a list that cannot be read must not open the document
		}
		email := ""
		if who != nil {
			email = who(r)
		}
		return Role(acl.RoleOf(email))
	}
}

// AuthUser makes the User function of a Config from an auth.Manager: the
// e-mail address of the signed-in caller (a bearer token), or "".
func AuthUser(m *auth.Manager) func(*http.Request) string {
	return func(r *http.Request) string {
		if u, ok := m.CurrentUser(r); ok {
			return u.Email
		}
		return ""
	}
}
