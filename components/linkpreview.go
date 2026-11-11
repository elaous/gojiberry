package components

import (
	"github.com/elaous/gojiberry/canvas"
	"net/url"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// LinkPreviewProps configures LinkPreview.
type LinkPreviewProps struct {
	// URL is the link's address.
	URL                                 string
	Title, Description, Image, SiteName string
	// Loading shows a placeholder while they are being fetched.
	Loading bool
	OnOpen  func()
	// OnClose, if set, adds a close button.
	OnClose func()
}

func LinkHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Host, "www.")
}

func LinkPreview(props LinkPreviewProps) xango.Node {
	if props.URL == "" {
		return xango.Frag()
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
	attrs := []xango.Attr{
		xango.A("class", class), xango.A("href", href), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer"),
	}
	if props.OnOpen != nil {
		attrs = append(attrs, xango.On("click", func(e xango.Event) {
			e.PreventDefault()
			props.OnOpen()
		}))
	}
	var body []xango.Node
	if props.Image != "" {
		body = append(body, xango.El("img", []xango.Attr{xango.A("class", "gj-linkpreview-image"), xango.A("src", props.Image), xango.A("alt", "")}))
	}
	text := []xango.Node{
		xango.El("span", []xango.Attr{xango.A("class", "gj-linkpreview-title")}, xango.Text(title)),
	}
	if props.Description != "" && !props.Loading {
		text = append(text, xango.El("span", []xango.Attr{xango.A("class", "gj-linkpreview-description")}, xango.Text(props.Description)))
	}
	text = append(text, xango.El("span", []xango.Attr{xango.A("class", "gj-linkpreview-site")}, xango.Text(site)))
	body = append(body, xango.El("span", []xango.Attr{xango.A("class", "gj-linkpreview-text")}, text...))
	card := xango.El("a", attrs, body...)
	if props.OnClose == nil {
		return card
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-linkpreview-wrap")}, card,
		xango.El("button", []xango.Attr{
			xango.A("class", "gj-linkpreview-close"), xango.A("type", "button"), xango.A("aria-label", "Close preview"),
			xango.On("click", func(xango.Event) { props.OnClose() }),
		}, xango.Text("×")))
}
