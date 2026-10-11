//go:build js && wasm

package main

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// reviewPanel is the Review tab: suggestion mode on or off, and the suggested changes to accept or
// reject one by one or all at once. Only someone who can edit settles them (a commenter or viewer sees
// the list).
func reviewPanel(u *ui, state *State, flow func() *canvas.TextFlow, redraw, refocus, renderUI func()) xango.Node {
	fl := flow()
	if fl == nil {
		return xango.Frag()
	}
	canEdit := state.Role.CanEdit()
	done := func(msg string) {
		fl.Touch() // tell a shared document about it
		u.announce(msg)
		redraw()
		refocus()
		renderUI()
	}
	p := components.ProposalPanelProps{Proposals: fl.Proposals(), Suggesting: fl.Suggesting}
	if canEdit {
		p.OnMode = func(on bool) {
			fl.Suggesting = on
			if on {
				u.announce(T("review.modeOn", "Suggesting: your edits are suggestions"))
			} else {
				u.announce(T("review.modeOff", "Editing: your edits are made directly"))
			}
			renderUI()
		}
		p.OnAccept = func(id string) {
			fl.AcceptProposal(id)
			done(T("review.accepted", "Suggestion accepted"))
		}
		p.OnReject = func(id string) {
			fl.RejectProposal(id)
			done(T("review.rejected", "Suggestion rejected"))
		}
		p.OnAcceptAll = func() {
			n := fl.AcceptAllProposals()
			done(Tn("review.acceptedAll", "%d suggestion accepted", "%d suggestions accepted", n))
		}
		p.OnRejectAll = func() {
			n := fl.RejectAllProposals()
			done(Tn("review.rejectedAll", "%d suggestion rejected", "%d suggestions rejected", n))
		}
	}
	return components.ProposalPanel(p)
}

// proposalsSigFor is proposalsSig while the Review tab is open (the other tabs do not pay for it).
func proposalsSigFor(tab string, fl *canvas.TextFlow) string {
	if tab != "review" {
		return ""
	}
	return proposalsSig(fl)
}

// proposalsSig changes when the suggested changes do (so the Review tab follows the typing).
func proposalsSig(fl *canvas.TextFlow) string {
	if fl == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(strconv.FormatBool(fl.Suggesting))
	for _, p := range fl.Proposals() {
		sb.WriteString("|" + p.ID + ":" + p.Inserted + ":" + p.Deleted)
	}
	return sb.String()
}
