package components

import (
	"testing"

	"github.com/elaous/xango"
)

func TestTheme(t *testing.T) {
	t.Run("default Mode renders the light class", func(t *testing.T) {
		html := render(t, Theme(ThemeProps{}))
		mustContain(t, html, "gj-theme-light")
		mustNotContain(t, html, "gj-theme-dark")
	})

	t.Run("ThemeDark renders the dark class", func(t *testing.T) {
		html := render(t, Theme(ThemeProps{Mode: ThemeDark}))
		mustContain(t, html, "gj-theme-dark")
	})

	t.Run("Children render inside the wrapper", func(t *testing.T) {
		html := render(t, Theme(ThemeProps{Children: []xango.Node{xango.Text("hello")}}))
		mustContain(t, html, "hello")
	})

	t.Run("Custom overrides render as an inline style, sorted for determinism", func(t *testing.T) {
		html := render(t, Theme(ThemeProps{Custom: map[string]string{"--gj-bg": "#000", "--gj-accent": "#10b981"}}))
		mustContain(t, html, `style="--gj-accent: #10b981; --gj-bg: #000;"`)
	})

	t.Run("no Custom omits the style attribute", func(t *testing.T) {
		html := render(t, Theme(ThemeProps{}))
		mustNotContain(t, html, "style=")
	})
}
