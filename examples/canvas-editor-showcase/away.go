//go:build js && wasm

package main

import (
	"time"

	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/xango"
)

type awayState struct {
	wasOnline bool              // the connection has been up once
	offline   bool              // and has gone down since
	base      *docfile.Document // the text when the connection came back
	changes   []docdiff.Change  // what the others changed meanwhile (empty: nothing to show)
	timer     bool
}

func (u *ui) noteAway(status client.Status, renderUI func()) {
	a := &u.away
	switch {
	case status == client.Online && !a.wasOnline:
		a.wasOnline = true
	case status == client.Offline && a.wasOnline:
		a.offline = true
	case status != client.Offline && a.offline && a.base == nil && u.current != nil:
		d := u.current()
		a.base, a.offline = &d, false
	}
	if status == client.Online && a.base != nil && !a.timer {
		a.timer = true
		time.AfterFunc(2500*time.Millisecond, func() {
			a.timer = false
			if a.base == nil || u.current == nil {
				return
			}
			a.changes = docdiff.Compare(*a.base, u.current())
			a.base = nil
			if docdiff.Summarize(a.changes).Changed() {
				u.announce(T("away.said", "Others changed the document while you were offline. The changes are listed in the sharing panel."))
			}
			renderUI()
		})
	}
}

func (u *ui) awayPanel(renderUI func()) (xango.Node, bool) {
	if !docdiff.Summarize(u.away.changes).Changed() {
		return xango.Node{}, false
	}
	return xango.El("section", []xango.Attr{xango.A("id", "away"), xango.A("aria-labelledby", "away-title"), xango.A("class", "gj-away"), xango.A("style", "margin-bottom:0.8em")},
		xango.El("h4", []xango.Attr{xango.A("id", "away-title")}, xango.Text(T("away.title", "While you were offline"))),
		components.VersionDiff(components.VersionDiffProps{ID: "away-diff", Label: T("away.label", "Changes made by others while you were offline"), Changes: u.away.changes}),
		components.Button(components.ButtonProps{Label: T("away.dismiss", "Dismiss"), Variant: components.ButtonSecondary, OnClick: func(xango.Event) {
			u.away.changes = nil
			renderUI()
		}})), true
}
