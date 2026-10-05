// Package user is the one description of a person the rest of the library shares:
// who has access to a document (docstore's ACL), who is listed in a People panel
// (components), and who is signed in. Those used to be separate, near-identical
// types.
package user

import "strings"

// Roles a person can have on a document.
const (
	Owner  = "owner"
	Editor = "editor"
	Viewer = "viewer"
)

// Person is someone with access to a document. ID is how they are known (an
// address, compared by Normalize); Name is what to show, ID if empty; Role is
// Owner, Editor or Viewer.
type Person struct {
	ID, Name, Role string
}

// DisplayName is the Name, or the ID when there is none.
func (p Person) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// Normalize is how an address is kept and compared: trimmed and in lower case, so
// "Ana@Gojiberry.io " and "ana@gojiberry.io" are one person.
func Normalize(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// Same reports whether two addresses are the same person.
func Same(a, b string) bool { return a != "" && Normalize(a) == Normalize(b) }
