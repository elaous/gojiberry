//go:build !(js && wasm)

package main

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestFindAndReplace(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.OK(!d.State().Bool("find.open"), "the find bar is closed at first")

	d.Focus("#input")
	d.Press("Control+f")
	d.Wait("s.find.open")
	d.OK(d.Count(".gj-find") == 1, "Ctrl+F opens the find bar, even in view mode")
	d.OK(d.Count(".gj-find-replacement") == 0, "and a viewer-mode document has no replace row")

	d.Fill(".gj-find-input", "the")
	d.Wait("s.find.count > 3")
	all := d.State().Int("find.count")
	status := d.Text(".gj-find-status")
	d.OK(regexp.MustCompile(`^1 of \d+$`).MatchString(status), "the bar shows which match is current: "+status)
	d.Click(".gj-find-next")
	d.Wait("s.find.current === 1")
	d.OK(d.State().Int("find.current") == 1, "Next goes to the second match")
	d.Click(".gj-find-prev")
	d.Click(".gj-find-prev")
	d.Wait(fmt.Sprintf("s.find.current === %d", all-1))
	d.OK(true, "Previous wraps round to the last")

	d.Click(".gj-find-word")
	d.Wait(fmt.Sprintf("s.find.count < %d", all))
	d.OK(d.State().Int("find.count") < all, "whole word narrows the matches")
	d.Click(".gj-find-word")

	d.Click(".gj-find-regex")
	d.Fill(".gj-find-input", "(")
	d.Wait("s.find.error !== ''")
	d.OK(d.Count(".gj-find-error") == 1, "a bad regular expression is shown as an error")
	d.Click(".gj-find-regex")

	d.ToggleMode()
	d.Focus("#input")
	d.Press("Control+h")
	d.Fill(".gj-find-input", "canvas")
	d.Wait("s.find.count >= 1")
	before := d.State().Str("text")
	n := d.State().Int("find.count")
	d.Fill(".gj-find-replacement", "CANVAS")
	d.Click(".gj-find-replace-all")
	d.Wait("s.text.includes('CANVAS')")
	after := d.State().Str("text")
	d.OK(after == regexp.MustCompile(`(?i)canvas`).ReplaceAllString(before, "CANVAS"), fmt.Sprintf("Replace all changed all %d matches and nothing else", n))

	d.CanvasClick(240, 164) // back into the text
	d.Focus("#input")
	d.Press("Control+z")
	d.Wait("s.text === " + e2etest.J(before))
	d.OK(true, "Undo brings the text back in one step")

	d.Click(".gj-find-close")
	d.OK(!d.State().Bool("find.open") && d.Count(".gj-find") == 0, "Close removes the bar")
	d.NoErrors()
}
