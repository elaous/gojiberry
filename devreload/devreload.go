// Package devreload lets a gox SSR server opt into a live-reload dev
// loop with two calls: wrap rendered HTML with (*Reloader).Wrap and
// mount (*Reloader).Handler wherever the injected script polls. A
// browser tab reloads itself once that endpoint reports a different
// version than the one the page was rendered with -- i.e. once the
// process serving it has been rebuilt and restarted, the other half of
// the loop devwatch (see the sibling devwatch package) drives.
//
// This began as examples/canvas-app's own hand-rolled devVersion/
// devReloadScript pair, wired into that app's net/http mux by hand;
// this package is that same mechanism generalized so any gox server
// (net/http, Gin, Echo, Encore's raw endpoints, ...) can use it without
// re-deriving it, matching gox's own opt-in-per-call style rather than
// a magic global. A Reloader is nil-safe everywhere a *Reloader is
// accepted (Wrap becomes a no-op, Handler serves 404) specifically so
// callers can keep one code path and pass nil in production instead of
// branching on a devMode bool at every call site -- see canvas-app's
// devserver for exactly that usage.
package devreload

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Reloader tracks one server process's identity for the dev-reload
// loop. The zero value is not usable; construct with New.
type Reloader struct {
	version string
}

// New constructs a Reloader whose version is this process's own start
// time -- enough on its own to differ after every restart devwatch
// performs, no separate file or signal needed to bump it.
func New() *Reloader {
	return &Reloader{version: strconv.FormatInt(time.Now().UnixNano(), 10)}
}

// versionPath is the fixed endpoint Wrap's injected script polls and
// Handler serves. Not configurable -- one hardcoded, unadvertised path
// keeps both halves in sync with no wiring for callers to get wrong,
// and dev-only tooling has no compatibility surface worth the
// indirection of making it a parameter.
const versionPath = "/__dev/version"

// Wrap injects a small polling script into html, just before its
// closing </body> tag (appended at the end if html has none). The
// script fetches versionPath every 500ms and reloads the tab the first
// time the response differs from r's own version, treating a failed
// fetch (the old process has exited but the new one isn't listening
// yet) as "not ready", not "reload now". Wrap on a nil *Reloader
// returns html unchanged, so production code paths can pass a nil
// Reloader instead of branching on a devMode flag.
func (r *Reloader) Wrap(html string) string {
	if r == nil {
		return html
	}
	script := `<script>` + fmt.Sprintf(`
(function () {
	var v = %q;
	setInterval(function () {
		fetch(%q, { cache: "no-store" }).then(function (resp) {
			return resp.text();
		}).then(function (t) {
			if (t !== v) location.reload();
		}).catch(function () {});
	}, 500);
})();
`, r.version, versionPath) + `</script>`

	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return html + script
}

// Handler serves r's version at versionPath as an uncached, plain-text
// response, for whatever router the caller mounts it on. Calling
// Handler on a nil *Reloader returns a handler that always answers 404,
// so registering it unconditionally (dev or not) is safe -- production
// simply gets a 404 for a path nothing in a production build ever
// requests, rather than the caller needing to skip registration too.
func (r *Reloader) Handler() http.Handler {
	if r == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, r.version)
	})
}

// Path returns the fixed endpoint Handler must be mounted at (e.g.
// mux.Handle(devreload.Path, reloader.Handler())), so callers never
// need to hardcode "/__dev/version" themselves.
func Path() string {
	return versionPath
}
