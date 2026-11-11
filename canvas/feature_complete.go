package canvas

// CompleteFeature adds the command that asks for word suggestions.
type CompleteFeature struct {
	// OnOpen is called by complete.open.
	OnOpen func()
}

// CmdComplete opens the word suggestions.
const CmdComplete Command = "complete.open"

func (CompleteFeature) Name() string { return "complete" }

func (cf CompleteFeature) Install(r *Registry) {
	r.AddGlobalCommand(CmdComplete, "Suggest words", "Mod+Space", func(*CommandContext) bool {
		if cf.OnOpen == nil {
			return false
		}
		cf.OnOpen()
		return true
	})
}
