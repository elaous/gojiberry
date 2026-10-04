package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

// render renders n to an HTML string, failing the test on error --
// every component test in this package goes through this instead of
// asserting against the xango.Node tree directly, since HTML output is
// what a real page actually gets.
func render(t *testing.T, n xango.Node) string {
	t.Helper()
	html, err := xango.RenderString(n)
	if err != nil {
		t.Fatalf("RenderString: %v", err)
	}
	return html
}

// findHandlers walks n's whole tree and returns every event handler
// attached under the given event name (e.g. "click"), in document
// order. Tests use this to fire a component's wiring directly --
// verifying an OnClick/OnChange/etc. prop actually reaches a real
// xango.On(...) attribute in the rendered tree -- without needing a
// browser or domrt.
func findHandlers(n xango.Node, event string) []xango.EventHandler {
	var out []xango.EventHandler
	var walk func(xango.Node)
	walk = func(n xango.Node) {
		for _, a := range n.Attrs {
			if a.Name == event && a.Handler != nil {
				out = append(out, a.Handler)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

// mustContain fails the test if html doesn't contain every one of want.
func mustContain(t *testing.T, html string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("expected output to contain %q, got: %s", w, html)
		}
	}
}

// mustNotContain fails the test if html contains any of unwanted.
func mustNotContain(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(html, w) {
			t.Errorf("expected output to NOT contain %q, got: %s", w, html)
		}
	}
}
