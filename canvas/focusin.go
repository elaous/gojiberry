package canvas

// FocusEnterer is a component that starts editing when the keyboard arrives in it: a person who gets to the
// editor with Tab or a skip link, and has no pointer to click the text with, must be able to type.
type FocusEnterer interface {
	// FocusIn is called when the editor's input takes the focus; it reports whether anything changed (the
	// stage then draws again).
	FocusIn() bool
}

// FocusIn tells the components that the keyboard is in the editor.
func (s *Stage) FocusIn() bool {
	if s.EditTarget() != nil {
		return false // something is being edited already (the text, or a header or footer): the keys go there
	}
	changed := false
	for _, c := range s.Components {
		if f, ok := c.(FocusEnterer); ok && f.FocusIn() {
			changed = true
		}
	}
	return changed
}

// FocusIn implements FocusEnterer: in edit mode the text is edited from where the caret last was (the start of
// the text the first time), with no click.
func (f *TextFlow) FocusIn() bool {
	if f.mode != ModeEdit || f.editing || f.Painter == nil {
		return false
	}
	f.editing, f.caretOff = true, false
	return true
}

// FocusIn implements FocusEnterer: the view's content takes it.
func (sv *ScrollView) FocusIn() bool { return sv.content.FocusIn() }

var (
	_ FocusEnterer = (*TextFlow)(nil)
	_ FocusEnterer = (*ScrollView)(nil)
)
