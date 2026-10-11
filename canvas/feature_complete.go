package canvas

// CompleteFeature adds the command that asks for word suggestions at the caret. The canvas holds the
// text and the caret (TextFlow.EditText, Cursors, ReplaceRange); finding the candidates and showing
// them is the app's job (package complete, components.Suggestions), so the command calls back:
// Ctrl/Cmd+Space opens the list.
type CompleteFeature struct {
	// OnOpen is called by complete.open.
	OnOpen func()
}

// CmdComplete opens the word suggestions.
const CmdComplete Command = "complete.open"

// Name implements Feature.
func (CompleteFeature) Name() string { return "complete" }

// Install implements Feature.
func (cf CompleteFeature) Install(r *Registry) {
	r.AddGlobalCommand(CmdComplete, "Suggest words", "Mod+Space", func(*CommandContext) bool {
		if cf.OnOpen == nil {
			return false
		}
		cf.OnOpen()
		return true
	})
}
