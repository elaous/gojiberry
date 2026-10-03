package components

import (
	"github.com/elaous/gojiberry/canvas"
	"net/url"
	"strings"

	"github.com/elaous/gojiberry/gox"
)

// LinkPreviewProps configures LinkPreview.
type LinkPreviewProps struct {
	// URL is the link's address.
	URL string
	// Title, Description, Image and SiteName are what the page at URL
	// says about itself (Open Graph tags, say). A browser cannot read
	// them from another site, so the app fetches them on a server and
	// passes them in; anything left empty is simply not shown.
	Title, Description, Image, SiteName string
	// Loading shows a placeholder while they are being fetched.
	Loading bool
	// OnOpen is called when the card is clicked (otherwise it is an
	// ordinary link that opens in a new tab).
	OnOpen func()
	// OnClose, if set, adds a close button.
	OnClose func()
}

// LinkHost returns the host part of a URL for display ("example.com"),
// or the URL itself if it does not parse.
func LinkHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// LinkPreview renders a small card for a link: its image, title,
// description and site. With only a URL it shows the host; renders nothing
// without one. The card is itself a link to the URL.
func LinkPreview(props LinkPreviewProps) gox.Node {
	if props.URL == "" {
		return gox.Frag()
	}
	class := "gj-linkpreview"
	if props.Loading {
		class += " gj-linkpreview-loading"
	}
	title := props.Title
	if title == "" {
		title = LinkHost(props.URL)
	}
	site := props.SiteName
	if site == "" {
		site = LinkHost(props.URL)
	}
	href := props.URL
	if !canvas.SafeLinkURL(href) {
		href = "" // a javascript: or data: URL is never made a link
	}
	attrs := []gox.Attr{
		gox.A("class", class), gox.A("href", href), gox.A("target", "_blank"), gox.A("rel", "noopener noreferrer"),
	}
	if props.OnOpen != nil {
		attrs = append(attrs, gox.On("click", func(e gox.Event) {
			e.PreventDefault()
			props.OnOpen()
		}))
	}
	var body []gox.Node
	if props.Image != "" {
		body = append(body, gox.El("img", []gox.Attr{gox.A("class", "gj-linkpreview-image"), gox.A("src", props.Image), gox.A("alt", "")}))
	}
	text := []gox.Node{
		gox.El("span", []gox.Attr{gox.A("class", "gj-linkpreview-title")}, gox.Text(title)),
	}
	if props.Description != "" && !props.Loading {
		text = append(text, gox.El("span", []gox.Attr{gox.A("class", "gj-linkpreview-description")}, gox.Text(props.Description)))
	}
	text = append(text, gox.El("span", []gox.Attr{gox.A("class", "gj-linkpreview-site")}, gox.Text(site)))
	body = append(body, gox.El("span", []gox.Attr{gox.A("class", "gj-linkpreview-text")}, text...))
	card := gox.El("a", attrs, body...)
	if props.OnClose == nil {
		return card
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-linkpreview-wrap")}, card,
		gox.El("button", []gox.Attr{
			gox.A("class", "gj-linkpreview-close"), gox.A("type", "button"), gox.A("aria-label", "Close preview"),
			gox.On("click", func(gox.Event) { props.OnClose() }),
		}, gox.Text("×")))
}
