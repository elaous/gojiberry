//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestAboutTabShowsTheCreditAndTheSourceLink(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="about"]`)
	d.WaitSelector("#about-credit")
	d.OK(strings.Contains(d.Text("#about-credit"), "github.com/elaous/gojiberry"), "the credit names the repository")
	d.OK(strings.Contains(d.Text("#about-licence"), "Affero"), "the licence is named")
	d.OK(d.Attr("#about-source", "href") == "https://github.com/elaous/gojiberry", "the source link")
	d.OK(strings.HasSuffix(d.Attr("#about-licence-text", "href"), "/LICENSE"), "the licence link")
	d.OK(strings.HasSuffix(d.Attr("#about-commercial", "href"), "/COMMERCIAL.md"), "the commercial licence link")
	d.NoErrors()
}
