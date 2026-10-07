package main

import (
	"errors"
	"net/url"
	"strings"
)

// The page's own rules for what a link or a stored setting may point the key directory at, and for
// the word that confirms erasing. They have no browser code, so they are tested natively
// (policy_test.go).

// errDirURL is why an address was refused as a key directory.
var errDirURL = errors.New("a key directory must be an https address (http only for localhost), with no user name or password")

// checkDirURL decides whether raw may be used as the key directory and returns it cleaned (no
// trailing slash) with its host (the name the person is shown and asked to confirm). The directory
// gets the addresses looked up, the session token, and its answers are shown as verified, so a
// plain-http address (readable and replaceable on the way) and one that carries credentials are
// refused; http is allowed only for a host on this machine, for development.
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

// eraseWordMatches says whether what was typed is the word that confirms erasing: exactly, in the
// same case ("erase" is not "ERASE"), ignoring the spaces around it.
func eraseWordMatches(typed, word string) bool { return strings.TrimSpace(typed) == word }
