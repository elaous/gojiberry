// Package user is the one description of a person the rest of the library shares:
// who has access to a document (docstore's ACL), who is listed in a People panel
// (components), and who is signed in (auth, over OpenID Connect).
package user

import "strings"

// Roles a person can have on a document.
const (
	Owner  = "owner"
	Editor = "editor"
	Viewer = "viewer"
)

// Person is someone with access to a document, or who is signed in. The fields
// follow what an OpenID Connect provider tells (see auth):
//
//   - ID is the stable key: for a signed-in user "{provider}:{sub}", the provider's
//     own subject, which never changes; for someone invited by an address, that
//     address.
//   - Email is the verified address, "" if the provider gave none or it is not
//     verified. It can change, so it is how people are invited and found, not what
//     they are known by.
//   - Username is the provider's preferred_username, if any.
//   - Name and Picture (a URL) are for display.
//   - Role is Owner, Editor or Viewer.
type Person struct {
	ID, Email, Username, Name, Picture, Role string
}

// DisplayName is the Name, else the Username, else the Email, else the ID.
func (p Person) DisplayName() string {
	for _, s := range []string{p.Name, p.Username, p.Email} {
		if s != "" {
			return s
		}
	}
	return p.ID
}

// Identifiers are the ways the person can be found in an access list: ID, Email and
// Username, those that are set, each as Normalize writes it.
func (p Person) Identifiers() []string {
	var out []string
	for _, s := range []string{p.ID, p.Email, p.Username} {
		if n := Normalize(s); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Is reports whether id (an address, an ID or a username) names this person.
func (p Person) Is(id string) bool {
	n := Normalize(id)
	if n == "" {
		return false
	}
	for _, have := range p.Identifiers() {
		if have == n {
			return true
		}
	}
	return false
}

// Normalize is how an identifier is kept and compared: trimmed, and in lower case
// when it is an e-mail address ("Ana@Gojiberry.io " and "ana@gojiberry.io" are one
// person). Anything else is compared exactly: an OpenID Connect subject is
// case-sensitive.
func Normalize(id string) string {
	id = strings.TrimSpace(id)
	if strings.Contains(id, "@") {
		return strings.ToLower(id)
	}
	return id
}

// Same reports whether two identifiers are the same (see Normalize); empty is nobody.
func Same(a, b string) bool { return Normalize(a) != "" && Normalize(a) == Normalize(b) }
