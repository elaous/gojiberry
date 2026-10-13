package observe

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestRoutesOfTheOtherServersAreTemplatesOfTheAllowlist(t *testing.T) {
	for path, want := range map[string]string{
		"/healthz":                           "/health",
		"/docs/secret-id/events":             "/docs/{id}/events",
		"/docs/secret-id/snapshot":           "/docs/{id}/snapshot",
		"/docs/secret-id/ws":                 "/docs/{id}/ws",
		"/docs/secret-id/ops":                "/docs/{id}/ops",
		"/docs/secret-id/presence":           "/docs/{id}/presence",
		"/docs/secret-id/role":               "/docs/{id}/role",
		"/ops":                               "/ops",
		"/presence":                          "/presence",
		"/local/files/Some Private Font.ttf": "/local/*",
		"/local/fonts":                       "/local/*",
		"/google/fonts":                      "/google/*",
		"/translate":                         "/translate",
		"/speak":                             "/speak",
		"/transcribe":                        "/transcribe",
	} {
		got := RouteOf(path)
		if got != want || !slices.Contains(labelValues["route"], got) {
			t.Errorf("RouteOf(%q) = %q, want %q (in the allowlist: %v)", path, got, want, slices.Contains(labelValues["route"], got))
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "Private") {
			t.Errorf("an identifier reached the route %q", got)
		}
	}
}

// A WebSocket upgrade (the collaboration hub's /docs/{id}/ws) must still reach the real connection
// through the middleware's writer.
func TestMiddlewareLetsAConnectionBeHijacked(t *testing.T) {
	o, _ := newObs()
	h := Middleware(o, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("Hijack through the middleware: %v", err)
			return
		}
		_, _ = c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
		_ = c.Close()
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("GET /docs/x/ws HTTP/1.1\r\nHost: x\r\n\r\n"))
	line, _ := bufio.NewReader(c).ReadString('\n')
	if !strings.Contains(line, "101") {
		t.Errorf("got %q", line)
	}
}

func TestSetupIsNilAndSilentWhenOff(t *testing.T) {
	o, err := Setup(context.Background(), func(string) string { return "" }, "gojiberry-fontserver")
	if o != nil || err != nil {
		t.Fatalf("off: %v %v", o, err)
	}
	if _, err := Setup(context.Background(), func(k string) string {
		return map[string]string{"GOJIBERRY_OBS": "on", "GOJIBERRY_OBS_SINKS": "nonsense"}[k]
	}, "gojiberry-fontserver"); err == nil {
		t.Error("a bad sink is an error")
	}
}
