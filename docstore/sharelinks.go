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

// Share links (roadmap 75). A link is a secret token the owner hands out; whoever sends it in the
// ShareHeader gets the link's role on that one document, until the link expires or the owner revokes it.
// The access list keeps only a hash of the token (so a copy of the list is not a copy of the links) and
// the role and deadline. A link never gives more than the document itself: not the access list, and not
// deletion. The owner and per-person grants are unchanged and are checked first; a link only adds
// access, it never reduces or replaces what a person was granted.

// ShareHeader is the request header that carries a share link's token.
const ShareHeader = "X-Gojiberry-Share"

// Limits of share links.
const (
	// DefaultLinkLife is how long a link lasts when the owner does not say.
	DefaultLinkLife = 7 * 24 * time.Hour
	// MaxLinkLife is the longest a link may last: a link without an end is not offered.
	MaxLinkLife = 90 * 24 * time.Hour
	// MinLinkLife is the shortest a link may last.
	MinLinkLife = time.Minute
	// MaxLinks is how many live links one document may have.
	MaxLinks = 20
)

// Link is one share link as the access list keeps it (the key of ACL.Links is the hash of its token).
type Link struct {
	Role    Role      `json:"role"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

// Live reports whether the link still works at now. A link works strictly before its deadline.
func (l Link) Live(now time.Time) bool { return now.Before(l.Expires) }

// NewShareToken is a new secret for a link: 32 random bytes, URL-safe.
func NewShareToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LinkID is what the access list calls a token: a hash, so the list cannot be turned back into links.
func LinkID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AddLink makes a link for token with the role, lasting life (DefaultLinkLife if zero) from now. The role
// is viewer or editor; life must be between MinLinkLife and MaxLinkLife; expired links are dropped first,
// and no more than MaxLinks live ones are kept. It returns the link's id.
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

// RevokeLink ends the link with that id; it reports whether there was one.
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

// LinkRole is the role the token gives at now: "" when it matches no link, the link was revoked, or it
// has expired.
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

// Viewer is one line of the "who can see this" list.
type Viewer struct {
	// Kind is "owner", "person" or "link".
	Kind string `json:"kind"`
	// ID is the address of a person, or the id of a link (to revoke it by).
	ID   string `json:"id"`
	Role string `json:"role"`
	// Expires is when a link ends; zero for the owner and for people.
	Expires time.Time `json:"expires,omitzero"`
}

// WhoCanSee lists everyone and everything that can open the document at now: the owner, the people
// granted a role, and the links still alive (an expired link is not listed: it opens nothing).
func (a ACL) WhoCanSee(now time.Time) []Viewer {
	var out []Viewer
	for _, p := range a.People() {
		kind := "person"
		if p.Role == "owner" {
			kind = "owner"
		}
		out = append(out, Viewer{Kind: kind, ID: p.ID, Role: p.Role})
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

// clock is the server's time: time.Now unless WithClock set another.
func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// WithClock sets the clock the deadlines of share links are read from (for tests).
func (s *Server) WithClock(now func() time.Time) *Server {
	s.now = now
	return s
}
