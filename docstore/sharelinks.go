package docstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

// ShareHeader is the request header carrying a share token.
const ShareHeader = "X-Gojiberry-Share"

// Limits of share links.
const (
	// DefaultLinkLife is how long a link lasts by default.
	DefaultLinkLife = 7 * 24 * time.Hour
	// MaxLinkLife is the longest a link may last.
	MaxLinkLife = 90 * 24 * time.Hour
	// MinLinkLife is the shortest a link may last.
	MinLinkLife = time.Minute
	// MaxLinks is how many live links one document may have.
	MaxLinks = 20
)

type Link struct {
	Role    Role      `json:"role"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

// Live reports whether the link still works.
func (l Link) Live(now time.Time) bool { return now.Before(l.Expires) }

// NewShareToken is a new secret for a link.
func NewShareToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LinkID is what the access list calls a link.
func LinkID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AddLink makes a link for token with the given life.
func (a *ACL) AddLink(token string, role Role, now time.Time, life time.Duration) (string, Link, error) {
	switch {
	case len(token) < 32:
		return "", Link{}, errors.New("docstore: a share token is too short")
	case role != RoleEditor && role != RoleViewer:
		return "", Link{}, errors.New("docstore: a link gives the role editor or viewer")
	}
	if life == 0 {
		life = DefaultLinkLife
	}
	if life < MinLinkLife || life > MaxLinkLife {
		return "", Link{}, errors.New("docstore: a link lasts between a minute and 90 days")
	}
	a.PruneLinks(now)
	if len(a.Links) >= MaxLinks {
		return "", Link{}, errors.New("docstore: this document has too many share links; revoke one first")
	}
	if a.Links == nil {
		a.Links = map[string]Link{}
	}
	l := Link{Role: role, Created: now.UTC().Truncate(time.Second), Expires: now.Add(life).UTC().Truncate(time.Second)}
	id := LinkID(token)
	a.Links[id] = l
	return id, l, nil
}

// RevokeLink ends the link with that id.
func (a *ACL) RevokeLink(id string) bool {
	_, ok := a.Links[id]
	delete(a.Links, id)
	return ok
}

// PruneLinks drops the links that have expired.
func (a *ACL) PruneLinks(now time.Time) {
	for id, l := range a.Links {
		if !l.Live(now) {
			delete(a.Links, id)
		}
	}
}

// LinkRole is the role the token gives.
func (a ACL) LinkRole(token string, now time.Time) Role {
	if token == "" {
		return ""
	}
	l, ok := a.Links[LinkID(token)]
	if !ok || !l.Live(now) {
		return ""
	}
	return l.Role
}

// Viewer is one line of the who-can-see list.
type Viewer struct {
	// Kind is "owner", "person" or "link".
	Kind string `json:"kind"`
	// ID is the person's address or the link's id.
	ID   string `json:"id"`
	Role string `json:"role"`
	// Expires is when a link or a person's access ends.
	Expires time.Time `json:"expires,omitzero"`
}

// WhoCanSee lists everyone and everything with access.
func (a ACL) WhoCanSee(now time.Time) []Viewer {
	a = a.Active(now)
	var out []Viewer
	for _, p := range a.People() {
		kind := "person"
		if p.Role == "owner" {
			kind = "owner"
		}
		v := Viewer{Kind: kind, ID: p.ID, Role: p.Role}
		if end, ok := a.Until[NormalizeAddress(p.ID)]; ok && kind == "person" {
			v.Expires = end
		}
		out = append(out, v)
	}
	var links []Viewer
	for id, l := range a.Links {
		if l.Live(now) {
			links = append(links, Viewer{Kind: "link", ID: id, Role: string(l.Role), Expires: l.Expires})
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if !links[i].Expires.Equal(links[j].Expires) {
			return links[i].Expires.Before(links[j].Expires)
		}
		return links[i].ID < links[j].ID
	})
	return append(out, links...)
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// WithClock sets the clock the deadlines use.
func (s *Server) WithClock(now func() time.Time) *Server {
	s.now = now
	return s
}
