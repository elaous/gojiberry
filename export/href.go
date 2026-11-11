package export

import "strings"

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
