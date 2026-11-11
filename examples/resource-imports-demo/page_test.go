package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestConfigIsTheDecodedConfigJSON(t *testing.T) {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var want SiteConfig
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if want.Title == "" {
		t.Fatal("config.json has no title; the test would prove nothing")
	}
	if config != want {
		t.Errorf("config = %+v, config.json says %+v", config, want)
	}
}

func TestPageUsesTheGivenConfigAndEscapesIt(t *testing.T) {
	got, err := xango.RenderString(Page(SiteConfig{Title: `<b>"A" & B</b>`}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<b>") || !strings.Contains(got, "<h1>&lt;b&gt;") || !strings.Contains(got, "&amp; B") {
		t.Errorf("title not escaped in the heading: %s", got)
	}
	// The title annotation wraps the page, stylesheet in head.
	if !strings.HasPrefix(got, "<html><head><title>resource-imports-demo</title><style ") || !strings.HasSuffix(got, "</body></html>") {
		t.Errorf("not wrapped in a document: %s", got)
	}
}
