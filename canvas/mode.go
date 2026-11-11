package canvas

// Mode is whether a component is viewed or modified.
type Mode int

const (
	// ModeView (the zero value) renders the component read-only.
	ModeView Mode = iota
	// ModeEdit makes the component interactive.
	ModeEdit
)

func (m Mode) String() string {
	if m == ModeEdit {
		return "edit"
	}
	return "view"
}

// Component is what every canvas component implements.
type Component interface {
	Mode() Mode
	// SetMode switches mode and reports whether a redraw is needed.
	SetMode(Mode) bool
	Draw()
}

var _ Component = (*TableView)(nil)
