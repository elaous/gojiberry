package components

import (
	"strings"
	"unicode"

	"github.com/elaous/gojiberry/gox"
)

// PresencePeer is one person at the document.
type PresencePeer struct {
	// Name is shown (and its initials); Color is their cursor color; User
	// is who the server says they are (an e-mail address); Role is what
	// they may do ("owner", "editor", "viewer").
	Name, Color, User, Role string
}

// PresenceProps configures Presence.
type PresenceProps struct {
	// Self is you; Peers are everyone else.
	Self  PresencePeer
	Peers []PresencePeer
	// Status is the connection: "online", "connecting" or "offline" (""
	// shows nothing about the connection).
	Status string
	// Pending is how many of your changes have not reached the server yet.
	Pending int
	// Role is your own role, shown as a badge ("View only" for a viewer).
	Role string
}

func initials(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '@' || r == '.' || r == '_' || r == '-' }) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// Presence shows who else is in the document -- a round avatar per person in
// their cursor color, with their name and role as the tip -- the state of the
// connection, and, for a viewer, a "View only" badge. It is controlled; feed
// it from collab.Session.
func Presence(props PresenceProps) gox.Node {
	avatar := func(p PresencePeer, self bool) gox.Node {
		title := p.Name
		if title == "" {
			title = p.User
		}
		if p.Role != "" {
			title += " (" + p.Role + ")"
		}
		class := "gj-presence-avatar"
		if self {
			class += " gj-presence-self"
		}
		color := p.Color
		if color == "" {
			color = "#7c5cff"
		}
		return gox.El("span", []gox.Attr{gox.A("class", class), gox.A("title", title), gox.A("data-name", p.Name),
			gox.A("style", "background:"+color)}, gox.Text(initials(orString(p.Name, p.User))))
	}
	people := []gox.Node{avatar(props.Self, true)}
	for _, p := range props.Peers {
		people = append(people, avatar(p, false))
	}
	kids := []gox.Node{gox.El("span", []gox.Attr{gox.A("class", "gj-presence-people"), gox.A("role", "list"), gox.A("aria-label", "People here")}, people...)}
	if props.Status != "" {
		label := map[string]string{"online": "Online", "connecting": "Connecting…", "offline": "Offline"}[props.Status]
		if label == "" {
			label = props.Status
		}
		if props.Pending > 0 {
			label += " · " + itoa(props.Pending) + " waiting to send"
		}
		kids = append(kids, gox.El("span", []gox.Attr{gox.A("class", "gj-presence-status gj-presence-"+props.Status), gox.A("role", "status")},
			gox.El("span", []gox.Attr{gox.A("class", "gj-presence-dot")}), gox.Text(label)))
	}
	if props.Role == "viewer" {
		kids = append(kids, gox.El("span", []gox.Attr{gox.A("class", "gj-presence-badge")}, gox.Text("View only")))
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-presence")}, kids...)
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
