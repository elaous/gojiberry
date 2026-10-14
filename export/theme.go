package export

import (
	"strconv"
	"strings"
)

// docTheme is what the formats other than HTML take from a document's theme (Snapshot.Theme, the design-token
// CSS of package tokens): the text, page and link colours as six hex digits and the body size in points. A
// part that is missing or not a plain colour is left empty, and the format keeps its own.
type docTheme struct {
	text, background, link string  // "RRGGBB" or ""
	bodyPt                 float64 // 0: the format's own
}

// themeOf reads the snapshot's theme; the zero value (nothing to apply) when there is none or it is not a
// plain :root block of custom properties.
func themeOf(snap Snapshot) docTheme {
	css := snap.Theme
	if css == "" || !safeThemeCSS(css) {
		return docTheme{}
	}
	body := strings.TrimSuffix(strings.TrimPrefix(css, ":root{"), "}")
	var t docTheme
	for _, decl := range strings.Split(strings.TrimSuffix(body, ";"), ";") {
		name, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name, val = strings.TrimSpace(name), strings.TrimSpace(val)
		switch name {
		case "--gj-color-text":
			t.text = hexColour(val)
		case "--gj-color-background":
			t.background = hexColour(val)
		case "--gj-color-link":
			t.link = hexColour(val)
		case "--gj-font-1":
			if px, err := strconv.ParseFloat(strings.TrimSuffix(val, "px"), 64); err == nil && px >= 8 && px <= 72 {
				t.bodyPt = px * 0.75
			}
		}
	}
	return t
}

// hexColour is "RRGGBB" for "#rgb", "#rrggbb" and "rgb(r, g, b)", "" for anything else.
func hexColour(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "#") && (len(v) == 4 || len(v) == 7):
		h := v[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		for _, c := range h {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return ""
			}
		}
		return strings.ToUpper(h)
	case strings.HasPrefix(v, "rgb(") && strings.HasSuffix(v, ")"):
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(v, "rgb("), ")"), ",")
		if len(parts) != 3 {
			return ""
		}
		out := ""
		for _, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return ""
			}
			out += strings.ToUpper(string([]byte{"0123456789abcdef"[n>>4], "0123456789abcdef"[n&15]}))
		}
		return out
	}
	return ""
}

// rgb is the three channels of "RRGGBB".
func rgb(hex string) (r, g, b uint8) {
	n, _ := strconv.ParseUint(hex, 16, 32)
	return uint8(n >> 16), uint8(n >> 8), uint8(n)
}
