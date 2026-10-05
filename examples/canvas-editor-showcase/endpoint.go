package main

import (
	"net/url"
	"strings"
)

// allowedEndpoint decides whether an address given in the page's query string
// (?hub=, ?driveapi=, ?graphapi=, ?translate=, ?grammar=) may be used, and returns it
// ready to use. These knobs exist for development and for self-hosting a backend, but
// a link can set them: without a check, a crafted link sends the document text (or the
// access token typed into the Migrate tab) to whatever host it names. So an address is
// taken only if it is a path on the page's own origin, or at the page's own origin, or
// at a loopback host (a backend on the developer's machine); anything else is refused.
func allowedEndpoint(raw, pageOrigin string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return "", false
	}
	if u.Scheme == "" && u.Host == "" && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return strings.TrimSuffix(pageOrigin, "/") + raw, true // a path on this origin
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	origin := u.Scheme + "://" + u.Host
	if origin == strings.TrimSuffix(pageOrigin, "/") {
		return raw, true
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return raw, true
	}
	return "", false
}
