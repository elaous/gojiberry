package devreload

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Reloader tracks one server process's identity.
type Reloader struct {
	version string
}

// New constructs a Reloader with a fresh version.
func New() *Reloader {
	return &Reloader{version: strconv.FormatInt(time.Now().UnixNano(), 10)}
}

const versionPath = "/__dev/version"

// Wrap injects a small polling script into HTML pages.
func (r *Reloader) Wrap(html string) string {
	if r == nil {
		return html
	}
	script := `<script>` + fmt.Sprintf(`
(function () {
	var v = %q;
	setInterval(function () {
		fetch(%q, { cache: "no-store" }).then(function (resp) {
			// An error page (a proxy's 502 while the server restarts) is not a new version.
			if (!resp.ok) return null;
			return resp.text();
		}).then(function (t) {
			if (t !== null && t !== v) location.reload();
		}).catch(function () {});
	}, 500);
})();
`, r.version, versionPath) + `</script>`

	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return html + script
}

// Handler serves r's version at versionPath.
func (r *Reloader) Handler() http.Handler {
	if r == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, r.version)
	})
}

// Path returns the fixed endpoint Handler mounts.
func Path() string {
	return versionPath
}
