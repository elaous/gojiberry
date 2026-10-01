package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestPageRendersTypedItemsThroughTheRenderProp(t *testing.T) {
	html, err := gox.RenderString(Page([]Person{{"Ada", "Engineer"}, {"Grace", "Admiral"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<ul class="gj-list">`,
		`data-key="Ada"`, `<strong>Ada</strong>, Engineer`, `<strong>Grace</strong>, Admiral`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	empty, _ := gox.RenderString(Page(nil))
	if !strings.Contains(empty, "Nobody here yet.") || strings.Contains(empty, "<ul") {
		t.Errorf("empty page = %s", empty)
	}
}
