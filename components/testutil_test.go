package components

import (
	"strings"
	"testing"

	"github.com/elaous/xango"
)

func render(t *testing.T, n xango.Node) string {
	t.Helper()
	html, err := xango.RenderString(n)
	if err != nil {
		t.Fatalf("RenderString: %v", err)
	}
	return html
}

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

func mustContain(t *testing.T, html string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("expected output to contain %q, got: %s", w, html)
		}
	}
}

func mustNotContain(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(html, w) {
			t.Errorf("expected output to NOT contain %q, got: %s", w, html)
		}
	}
}
