//go:build js && wasm

package browser

import (
	"fmt"
	"net/url"
	"strings"
	"syscall/js"
	"time"
)

// CookieOptions configures Cookies.Set.
type CookieOptions struct {
	Path     string // "" defaults to the current path
	Domain   string // "" defaults to the current domain
	MaxAge   int
	Expires  time.Time // zero means unset; MaxAge takes precedence where browsers support both
	Secure   bool
	SameSite string // "Strict", "Lax", or "None"; "" leaves the browser's own default
}

// Cookies wraps document.cookie.
var Cookies cookieJar

type cookieJar struct{}

// All returns every cookie visible to this document.
func (cookieJar) All() map[string]string {
	raw := js.Global().Get("document").Get("cookie").String()
	out := map[string]string{}
	if raw == "" {
		return out
	}
	for _, pair := range strings.Split(raw, "; ") {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		// PathUnescape: '+' stays a plus
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
		out[name] = value
	}
	return out
}

func (j cookieJar) Get(name string) (string, bool) {
	v, ok := j.All()[name]
	return v, ok
}

// Set sets one cookie.
func (cookieJar) Set(name, value string, opts CookieOptions) {
	if !cookieTokenOK(name) || strings.ContainsAny(name, "= \t,") || !cookieTokenOK(opts.Path) || !cookieTokenOK(opts.Domain) || !cookieTokenOK(opts.SameSite) {
		return
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteString("=")
	b.WriteString(strings.ReplaceAll(url.QueryEscape(value), "+", "%20")) // a space as %20, so a literal '+' stays a plus
	if opts.Path != "" {
		fmt.Fprintf(&b, "; path=%s", opts.Path)
	}
	if opts.Domain != "" {
		fmt.Fprintf(&b, "; domain=%s", opts.Domain)
	}
	if opts.MaxAge != 0 {
		fmt.Fprintf(&b, "; max-age=%d", opts.MaxAge)
	}
	if !opts.Expires.IsZero() {
		b.WriteString("; expires=" + opts.Expires.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"))
	}
	if opts.Secure {
		b.WriteString("; secure")
	}
	if opts.SameSite != "" {
		fmt.Fprintf(&b, "; samesite=%s", opts.SameSite)
	}
	js.Global().Get("document").Set("cookie", b.String())
}

// Delete removes a cookie by setting it already expired.
func (j cookieJar) Delete(name string, path, domain string) {
	j.Set(name, "", CookieOptions{Path: path, Domain: domain, MaxAge: -1})
}

func cookieTokenOK(s string) bool {
	for _, r := range s {
		if r == ';' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
