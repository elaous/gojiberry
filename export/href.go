package export

import "strings"

// safeHref reports whether a link may be written into an exported file: a
// relative reference, #bookmark, or an http, https, mailto or tel URL. Other
// schemes (javascript:, data:, ...) are dropped, leaving the text unlinked.
func safeHref(u string) bool {
	u = strings.TrimSpace(u)
	i := strings.IndexAny(u, ":/?#")
	if i < 0 || u[i] != ':' {
		return true
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}
