//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestSkipLinksAreTheFirstStopAndMoveTheFocus(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	text := func() string { return d.Str(`(document.activeElement.textContent || '').trim()`) }
	d.WaitSelector(".gj-skip-link")
	d.Eval(`document.activeElement && document.activeElement.blur(); true`, nil)
	d.Press("Tab")
	d.OK(text() == "Skip to the document", "the first skip link: "+text())
	d.OK(d.Bool(`document.activeElement.getBoundingClientRect().left >= 0`), "it is on screen once focused")
	d.Press("Enter")
	d.OK(d.Bool(`document.activeElement.classList.contains('gj-docview-desk')`), "Enter moves the focus to the document area")
	d.Focus(".gj-skip-link:nth-child(2)")
	d.OK(text() == "Skip to the side panel", "the second skip link: "+text())
	d.Press("Enter")
	d.OK(d.Bool(`document.activeElement.classList.contains('gj-docview-sidebar')`), "Enter moves the focus to the side panel")
	d.NoErrors()
}
