package components

import (
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestGrid(t *testing.T) {
	t.Run("Children render inside the grid", func(t *testing.T) {
		html := render(t, Grid(GridProps{Children: []gox.Node{gox.Text("hello")}}))
		mustContain(t, html, "hello", "gj-grid")
	})

	t.Run("no Width defaults to filling the parent", func(t *testing.T) {
		html := render(t, Grid(GridProps{}))
		mustContain(t, html, `style="width:100%;grid-template-columns:repeat(auto-fill, minmax(200px, 1fr));"`)
	})

	t.Run("an explicit Width overrides the default", func(t *testing.T) {
		html := render(t, Grid(GridProps{Width: "600px"}))
		mustContain(t, html, "width:600px;")
		mustNotContain(t, html, "width:100%;")
	})

	t.Run("Columns overrides the default responsive track sizing", func(t *testing.T) {
		html := render(t, Grid(GridProps{Columns: "1fr 1fr 1fr"}))
		mustContain(t, html, "grid-template-columns:1fr 1fr 1fr;")
		mustNotContain(t, html, "auto-fill")
	})

	t.Run("Gap renders as the CSS gap property", func(t *testing.T) {
		html := render(t, Grid(GridProps{Gap: "1rem"}))
		mustContain(t, html, "gap:1rem;")
	})

	t.Run("no Gap omits the gap property entirely", func(t *testing.T) {
		html := render(t, Grid(GridProps{}))
		mustNotContain(t, html, "gap:")
	})
}
