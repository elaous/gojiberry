package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// ACLRoles is a RoleFunc that reads the document's access list.
func ACLRoles(store docstore.Adapter, who func(*http.Request) string) RoleFunc {
	return ACLRolesWith(store, who, Owner)
}

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
			return "" // An unreadable list grants nothing.
		}
		email := ""
		if who != nil {
			email = who(r)
		}
		return Role(acl.Active(time.Now()).RoleOf(email))
	}
}

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
