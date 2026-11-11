package main

import (
	"errors"
	"net/url"
	"strings"
)

var errDirURL = errors.New("a key directory must be an https address (http only for localhost), with no user name or password")

func checkDirURL(raw string) (clean, host string, err error) {
	raw = strings.TrimSpace(raw)
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", "", errDirURL
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
		default:
			return "", "", errDirURL
		}
	default:
		return "", "", errDirURL
	}
	u.Fragment, u.RawQuery = "", ""
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), u.Host, nil
}

func eraseWordMatches(typed, word string) bool { return strings.TrimSpace(typed) == word }
