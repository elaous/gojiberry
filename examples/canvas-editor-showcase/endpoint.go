package main

import (
	"net/url"
	"strings"
)

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
