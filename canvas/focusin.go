package canvas

// FocusEnterer starts editing when the keyboard arrives in it.
type FocusEnterer interface {
	// FocusIn is called when the editor's input takes focus.
	FocusIn() bool
}

// FocusIn tells the components that the keyboard is in the editor.
func (s *Stage) FocusIn() bool {
	if s.EditTarget() != nil {
		return false
	}
	changed := false
	for _, c := range s.Components {
		if f, ok := c.(FocusEnterer); ok && f.FocusIn() {
			changed = true
		}
	}
	return changed
}

// FocusIn in edit mode resumes editing at the last caret.
func (f *TextFlow) FocusIn() bool {
	if f.mode != ModeEdit || f.editing || f.Painter == nil {
		return false
	}
	f.editing, f.caretOff = true, false
	return true
}

func (sv *ScrollView) FocusIn() bool { return sv.content.FocusIn() }

var (
	_ FocusEnterer = (*TextFlow)(nil)
	_ FocusEnterer = (*ScrollView)(nil)
)
