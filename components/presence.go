package components

import (
	"strings"
	"unicode"

	"github.com/elaous/xango"
)

// PresencePeer is one person at the document.
type PresencePeer struct {
	Name, Color, User, Role string
}

// PresenceProps configures Presence.
type PresenceProps struct {
	// Self is you; Peers are everyone else.
	Self    PresencePeer
	Peers   []PresencePeer
	Status  string
	Pending int
	Role    string
	OnWhere func(PresencePeer)
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

func Presence(props PresenceProps) xango.Node {
	avatar := func(p PresencePeer, self bool) xango.Node {
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
		return xango.El("span", []xango.Attr{xango.A("class", class), xango.A("title", title), xango.A("data-name", p.Name),
			xango.A("style", "background:"+color)}, xango.Text(initials(orString(p.Name, p.User))))
	}
	people := []xango.Node{avatar(props.Self, true)}
	for _, p := range props.Peers {
		people = append(people, avatar(p, false))
	}
	kids := []xango.Node{xango.El("span", []xango.Attr{xango.A("class", "gj-presence-people"), xango.A("role", "list"), xango.A("aria-label", "People here")}, people...)}
	if props.OnWhere != nil && len(props.Peers) > 0 {
		var asks []xango.Node
		for _, p := range props.Peers {
			peer := p
			asks = append(asks, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-presence-where"), xango.A("data-name", peer.Name),
				xango.On("click", func(xango.Event) { props.OnWhere(peer) })},
				xango.Text(trf("presence.where", "Where is %s?", orString(peer.Name, peer.User)))))
		}
		kids = append(kids, xango.El("span", []xango.Attr{xango.A("class", "gj-presence-asks"), xango.A("role", "group"), xango.A("aria-label", tr("presence.whereGroup", "Where are the others"))}, asks...))
	}
	if props.Status != "" {
		label := map[string]string{"online": "Online", "connecting": "Connecting…", "offline": "Offline"}[props.Status]
		if label == "" {
			label = props.Status
		}
		if props.Pending > 0 {
			label += " · " + itoa(props.Pending) + " waiting to send"
		}
		kids = append(kids, xango.El("span", []xango.Attr{xango.A("class", "gj-presence-status gj-presence-"+props.Status), xango.A("role", "status")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-presence-dot")}), xango.Text(label)))
	}
	if props.Role == "viewer" {
		kids = append(kids, xango.El("span", []xango.Attr{xango.A("class", "gj-presence-badge")}, xango.Text("View only")))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-presence")}, kids...)
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
