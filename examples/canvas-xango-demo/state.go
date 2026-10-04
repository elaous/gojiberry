package main

import "github.com/elaous/gojiberry/canvas"

// State is everything the tree depends on.
type State struct {
	Mode   canvas.Mode
	Source string
	Pages  int
}

const lorem = "# Quarterly report\n\n" +
	"This whole canvas is a **xango** tree: `<canvas.TextFlow>` inside `<canvas.Canvas>`, with the page furniture " +
	"described by a render prop. Switch to edit mode and double-click a header to type in it; the text stays when the state changes.\n\n" +
	"The text flows over the sheets and carries on at the top of the next one when a page is full. " +
	"The first page has no page number; the later ones read \"Page n of total\".\n\n" +
	"More text follows so that the flow reaches the second and third sheets. More text follows so that the flow reaches the second and third sheets. " +
	"More text follows so that the flow reaches the second and third sheets. More text follows so that the flow reaches the second and third sheets. " +
	"More text follows so that the flow reaches the second and third sheets.\n\n" +
	"## Appendix\n\nThe third sheet's header is different: the template returns a new header from there on."
