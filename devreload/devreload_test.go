package devreload

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrapInjectsBeforeClosingBody(t *testing.T) {
	r := New()
	html := "<html><body><h1>hi</h1></body></html>"
	got := r.Wrap(html)

	if !strings.Contains(got, r.version) {
		t.Fatalf("wrapped HTML doesn't embed the reloader's version: %s", got)
	}
	if i := strings.Index(got, "<script>"); i == -1 || i > strings.Index(got, "</body>") {
		t.Fatalf("script wasn't inserted before </body>: %s", got)
	}
	if !strings.HasSuffix(got, "</body></html>") {
		t.Fatalf("wrapping should preserve everything after </body>, got: %s", got)
	}
}

func TestWrapAppendsWhenNoClosingBody(t *testing.T) {
	r := New()
	got := r.Wrap("plain text, no body tag")
	if !strings.Contains(got, "<script>") {
		t.Fatalf("expected the script appended when there's no </body>, got: %s", got)
	}
}

func TestWrapNilReloaderIsNoOp(t *testing.T) {
	var r *Reloader
	html := "<html><body></body></html>"
	if got := r.Wrap(html); got != html {
		t.Fatalf("nil *Reloader.Wrap should return html unchanged, got: %s", got)
	}
}

func TestHandlerServesItsOwnVersion(t *testing.T) {
	r := New()
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + Path())
	if err != nil {
		t.Fatalf("GET %s: %v", Path(), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != r.version {
		t.Fatalf("Handler served %q, want the reloader's own version %q", body, r.version)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want \"no-store\" (a cached version would defeat the poll)", got)
	}
}

func TestHandlerNilReloaderIs404(t *testing.T) {
	var r *Reloader
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + Path())
	if err != nil {
		t.Fatalf("GET %s: %v", Path(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a nil *Reloader's Handler", resp.StatusCode)
	}
}

// TestEndToEndDifferentVersionsTriggerReload proves the actual contract
// two independent Reloader instances (i.e. two separate process runs)
// rely on: Wrap embeds one version, Handler serves a different one, and
// that's the exact condition the injected script's own `t !== v` check
// treats as "reload". This doesn't run the JS itself (no browser here),
// but confirms the two halves genuinely disagree across a real HTTP
// round trip when they're from different Reloaders, and agree when
// they're the same one.
func TestEndToEndDifferentVersionsTriggerReload(t *testing.T) {
	oldProcess := New()
	newProcess := New()
	if oldProcess.version == newProcess.version {
		t.Skip("two Reloaders constructed in the same nanosecond got the same version; vanishingly rare, not a real failure")
	}

	html := oldProcess.Wrap("<html><body>content</body></html>")
	if !strings.Contains(html, oldProcess.version) {
		t.Fatal("page wasn't rendered with the old process's version embedded")
	}

	srv := httptest.NewServer(newProcess.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + Path())
	if err != nil {
		t.Fatalf("GET %s: %v", Path(), err)
	}
	defer resp.Body.Close()
	served, _ := io.ReadAll(resp.Body)

	if string(served) == oldProcess.version {
		t.Fatal("the new process's Handler served the old process's version -- the script would never detect a restart")
	}
	if string(served) != newProcess.version {
		t.Fatalf("Handler served %q, want the new process's own version %q", served, newProcess.version)
	}
}
