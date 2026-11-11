package user

import "strings"

// Roles a person can have on a document.
const (
	Owner  = "owner"
	Editor = "editor"
	Viewer = "viewer"
)

type Person struct {
	ID, Email, Username, Name, Picture, Role string
}

func (p Person) DisplayName() string {
	for _, s := range []string{p.Name, p.Username, p.Email} {
		if s != "" {
			return s
		}
	}
	return p.ID
}

func (p Person) AccessIDs() []string {
	var out []string
	for _, s := range []string{p.ID, p.Email} {
		if n := Normalize(s); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (p Person) Identifiers() []string {
	var out []string
	for _, s := range []string{p.ID, p.Email, p.Username} {
		if n := Normalize(s); n != "" {
			out = append(out, n)
		}
	}
	return out
}

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

func Normalize(id string) string {
	id = strings.TrimSpace(id)
	if strings.Contains(id, "@") {
		return strings.ToLower(id)
	}
	return id
}

func Same(a, b string) bool { return Normalize(a) != "" && Normalize(a) == Normalize(b) }
