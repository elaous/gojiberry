package canvas

// FlowTheme is the part of a document theme (package tokens) the canvas draws: the colour of the text, of the
// page it is on and of links. An empty colour leaves the canvas's own. Colours a run's format names itself still win.
type FlowTheme struct {
	Text, Background, Link string
}
