//go:build js && wasm

package main

import (
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/xango"
)

// Share links (roadmap 75) in the People tab. The model is docstore.ACL, the one a server enforces: a
// link has a role (view or edit) and an end, the list keeps only a hash of its token, an expired or revoked
// link opens nothing, and "who can see this" is built from the same list the server checks. Here the list
// lives in memory like the people above it; against a server, HTTPAdapter.CreateLink, RevokeLink and
// LoadACL do the same over HTTP.

// linkLives are the lifetimes the person can choose; there is no "never".
var linkLives = []struct {
	key  string
	life time.Duration
}{
	{"1h", time.Hour},
	{"1d", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

// linksUI is the state of the share links section.
type linksUI struct {
	people *peopleState
	role   docstore.Role // the role of the next link
	life   string        // a key of linkLives
	// created is the link just made, shown once: the token is not kept anywhere the page can show it again.
	created string
	// tried is the token being checked, and result what it gives.
	tried, result string
	err           string // why a link could not be made
	now           func() time.Time
}

func newLinks(p *peopleState) *linksUI {
	return &linksUI{people: p, role: docstore.RoleViewer, life: "7d", now: time.Now}
}

func (l *linksUI) lifetime() time.Duration {
	for _, c := range linkLives {
		if c.key == l.life {
			return c.life
		}
	}
	return docstore.DefaultLinkLife
}

// roleLabel says what a role lets a person do, in plain words.
func roleLabel(role string) string {
	switch role {
	case "owner":
		return T("links.role.owner", "Owner: full access")
	case string(docstore.RoleEditor):
		return T("links.role.edit", "Can edit")
	}
	return T("links.role.view", "Can view and comment")
}

func lifeLabel(key string) string {
	switch key {
	case "1h":
		return T("links.life.1h", "1 hour")
	case "1d":
		return T("links.life.1d", "1 day")
	case "30d":
		return T("links.life.30d", "30 days")
	}
	return T("links.life.7d", "7 days")
}

// when writes a deadline for a person: date and time to the minute.
func when(t time.Time) string { return t.Local().Format("2 Jan 2006 15:04") }

func (l *linksUI) create() {
	token, err := docstore.NewShareToken()
	if err != nil {
		l.err = err.Error()
		l.people.rerender()
		return
	}
	if _, _, err := l.people.acl.AddLink(token, l.role, l.now(), l.lifetime()); err != nil {
		l.err = err.Error()
		l.people.rerender()
		return
	}
	l.err = ""
	l.created = pageBase() + "#share=" + token
	l.people.rerender()
}

func (l *linksUI) check() {
	token := strings.TrimSpace(l.tried)
	if i := strings.LastIndex(token, "share="); i >= 0 {
		token = token[i+len("share="):]
	}
	now := l.now()
	role := l.people.acl.LinkRole(token, now)
	switch {
	case token == "":
		l.result = ""
	case role == "":
		l.result = T("links.refused", "This link does not work: it has expired, it was revoked, or it is not a link to this document.")
	default:
		l.result = T("links.works", "This link works: %s, until %s.", roleLabel(string(role)), when(l.people.acl.Links[docstore.LinkID(token)].Expires))
	}
	l.people.rerender()
}

func selectOf(id, label string, current string, values [][2]string, on func(string)) xango.Node {
	var opts []xango.Node
	for _, v := range values {
		attrs := []xango.Attr{xango.A("value", v[0])}
		if v[0] == current {
			attrs = append(attrs, xango.A("selected", "selected"))
		}
		opts = append(opts, xango.El("option", attrs, xango.Text(v[1])))
	}
	return xango.El("label", []xango.Attr{xango.A("style", "display:block;margin:4px 0")}, xango.Text(label+" "),
		xango.El("select", []xango.Attr{xango.A("id", id), xango.On("change", func(e xango.Event) { on(xango.EventValue[string](e)) })}, opts...))
}

// panel is the section: make a link, check one, and the list of who can see the document.
func (l *linksUI) panel() xango.Node {
	now := l.now()
	acl := l.people.acl
	acl.PruneLinks(now) // an expired link drops off the list as soon as it ends (acl is a copy of the struct; the map is shared)
	var lives [][2]string
	for _, c := range linkLives {
		lives = append(lives, [2]string{c.key, lifeLabel(c.key)})
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("id", "links-heading")}, xango.Text(T("links.heading", "Share with a link"))),
		xango.El("p", []xango.Attr{xango.A("id", "links-intro"), xango.A("style", "font-size:13px")}, xango.Text(T("links.intro", "Anyone who has the link gets the access you choose, until it ends or you revoke it. A link can never change who has access, and never deletes the document. Commenting on selected text is part of Can view; a separate comment-only role is not built yet."))),
		xango.El("fieldset", []xango.Attr{xango.A("id", "links-new"), xango.A("style", "border:0;padding:0;margin:6px 0")},
			xango.El("legend", nil, xango.Text(T("links.new", "New link"))),
			selectOf("link-role", T("links.accessLabel", "Access"), string(l.role), [][2]string{
				{string(docstore.RoleViewer), roleLabel(string(docstore.RoleViewer))},
				{string(docstore.RoleEditor), roleLabel(string(docstore.RoleEditor))},
			}, func(v string) { l.role = docstore.Role(v) }),
			selectOf("link-life", T("links.lifeLabel", "Ends after"), l.life, lives, func(v string) { l.life = v }),
			btn("link-create", T("links.create", "Create link"), l.create, false)),
	}
	if l.err != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "link-error"), xango.A("role", "alert")}, xango.Text(l.err)))
	}
	if l.created != "" {
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "link-new-box"), xango.A("role", "status")},
			xango.El("label", []xango.Attr{xango.A("for", "link-new-url")}, xango.Text(T("links.once", "Your new link. It is shown only now; copy it before you leave."))),
			xango.El("input", []xango.Attr{xango.A("id", "link-new-url"), xango.A("readonly", "readonly"), xango.A("style", "width:100%"), xango.A("value", l.created)}),
			btn("link-dismiss", T("links.done", "Done"), func() { l.created = ""; l.people.rerender() }, false)))
	}
	nodes = append(nodes,
		xango.El("h4", []xango.Attr{xango.A("id", "link-check-heading")}, xango.Text(T("links.checkHeading", "Check a link"))),
		xango.El("label", []xango.Attr{xango.A("for", "link-try"), xango.A("style", "display:block;font-size:13px")}, xango.Text(T("links.tryLabel", "Paste a link to see what it gives right now"))),
		xango.El("input", []xango.Attr{xango.A("id", "link-try"), xango.A("style", "width:100%"), xango.A("value", l.tried),
			xango.On("input", func(e xango.Event) { l.tried = xango.EventValue[string](e) })}),
		btn("link-check", T("links.check", "Check"), l.check, false),
		xango.El("p", []xango.Attr{xango.A("id", "link-result"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(l.result)),
		xango.El("h4", []xango.Attr{xango.A("id", "who-heading")}, xango.Text(T("links.whoHeading", "Who can see this"))),
	)
	var items []xango.Node
	for _, v := range acl.WhoCanSee(now) {
		var text string
		var extra []xango.Node
		switch v.Kind {
		case "link":
			text = T("links.item", "Link ending %s: %s", when(v.Expires), roleLabel(v.Role))
			id := v.ID
			extra = append(extra, btn("revoke-"+id[:12], T("links.revoke", "Revoke"), func() {
				l.people.acl.RevokeLink(id)
				l.result = ""
				l.people.rerender()
			}, false))
		case "owner":
			text = v.ID + ": " + roleLabel("owner")
		default:
			text = v.ID + ": " + roleLabel(v.Role)
		}
		attrs := []xango.Attr{xango.A("data-kind", v.Kind), xango.A("data-role", v.Role)}
		if v.Kind == "link" {
			attrs = append(attrs, xango.A("data-expires", v.Expires.UTC().Format(time.RFC3339)))
		}
		items = append(items, xango.El("li", attrs, append([]xango.Node{xango.Text(text + " ")}, extra...)...))
	}
	nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "who-list"), xango.A("aria-labelledby", "who-heading"), xango.A("style", "font-size:13px")}, items...))
	return xango.El("section", []xango.Attr{xango.A("id", "links"), xango.A("aria-labelledby", "links-heading")}, nodes...)
}

// state is what #state shows of the links, for the tests.
func (l *linksUI) state() map[string]any {
	var out []map[string]any
	for _, v := range l.people.acl.WhoCanSee(l.now()) {
		if v.Kind == "link" {
			out = append(out, map[string]any{"role": v.Role, "expires": v.Expires.UTC().Format(time.RFC3339)})
		}
	}
	return map[string]any{"links": out}
}
