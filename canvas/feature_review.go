package canvas

// Review adds the commands of suggestion mode (see TextFlow.Suggesting and Proposals):
//
//   - Ctrl/Cmd+Alt+S turns suggestion mode on or off (the same key both ways);
//   - Ctrl/Cmd+Alt+Y accepts the suggestion at the caret, or the next one after it;
//   - Ctrl/Cmd+Alt+D declines (rejects) it.
//
// They need a TextFlow as the target.
type Review struct {
	// OnMode, when set, is told when suggestion mode is turned on or off (to announce it).
	OnMode func(on bool)
	// OnSettle, when set, is told which suggestion was accepted or rejected.
	OnSettle func(accepted bool, p Proposal)
}

// Command names.
const (
	CmdReviewSuggest Command = "review.suggest"
	CmdReviewAccept  Command = "review.accept"
	CmdReviewReject  Command = "review.reject"
)

// Name implements Feature.
func (Review) Name() string { return "review" }

// Install implements Feature.
func (rv Review) Install(r *Registry) {
	r.AddCommand(CmdReviewSuggest, "Suggest changes (on/off)", "Mod+Alt+S", func(c *CommandContext) bool {
		f, ok := c.Target.(*TextFlow)
		if !ok {
			return false
		}
		f.Suggesting = !f.Suggesting
		if rv.OnMode != nil {
			rv.OnMode(f.Suggesting)
		}
		return true
	})
	settle := func(accept bool) func(*CommandContext) bool {
		return func(c *CommandContext) bool {
			f, ok := c.Target.(*TextFlow)
			if !ok {
				return false
			}
			p, ok := f.ProposalNear(f.Cursors()[0].Caret)
			if !ok {
				return false
			}
			if accept {
				f.AcceptProposal(p.ID)
			} else {
				f.RejectProposal(p.ID)
			}
			if rv.OnSettle != nil {
				rv.OnSettle(accept, p)
			}
			return true
		}
	}
	r.AddCommand(CmdReviewAccept, "Accept suggestion", "Mod+Alt+Y", settle(true))
	r.AddCommand(CmdReviewReject, "Reject suggestion", "Mod+Alt+D", settle(false))
}

// ProposalNear is the suggestion that contains offset o (or ends at it), else the first one that
// starts after it, else the first one in the text.
func (f *TextFlow) ProposalNear(o int) (Proposal, bool) {
	ps := f.Proposals()
	for _, p := range ps {
		if o >= p.From && o <= p.To {
			return p, true
		}
	}
	for _, p := range ps {
		if p.From > o {
			return p, true
		}
	}
	if len(ps) > 0 {
		return ps[0], true
	}
	return Proposal{}, false
}
