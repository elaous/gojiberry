package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
	return ACLRolesWith(store, who, Owner)
}

// ACLRolesWith is ACLRoles with a choice for the documents that have no ACL:
// noACL is the role everyone gets on them (Owner is what ACLRoles gives, so a
// new document can be started by anyone; "" closes them, so only documents
// someone has shared by ACL can be opened at all; Viewer lets anyone read).
func ACLRolesWith(store docstore.Adapter, who func(*http.Request) string, noACL Role) RoleFunc {
	return func(r *http.Request, id string) Role {
		stored, err := store.Load(r.Context(), "acl:"+id)
		if errors.Is(err, docstore.ErrNotFound) {
			return noACL // none stored
		}
		if err != nil {
			return "" // an unreadable list must not open the document
		}
		var acl docstore.ACL
		if json.Unmarshal(stored.Data, &acl) != nil {
			return "" // a list that cannot be read must not open the document
		}
		email := ""
		if who != nil {
			email = who(r)
		}
		return Role(acl.Active(time.Now()).RoleOf(email))
	}
}

// AuthUser makes the User function of a Config from an auth.Manager: the
// verified e-mail address of the signed-in caller (a bearer token), else their
// ID, or "".
func AuthUser(m *auth.Manager) func(*http.Request) string {
	return func(r *http.Request) string {
		if u, ok := m.CurrentUser(r); ok {
			if u.Email != "" {
				return u.Email
			}
			return u.ID // no verified address: the provider's subject
		}
		return ""
	}
}
