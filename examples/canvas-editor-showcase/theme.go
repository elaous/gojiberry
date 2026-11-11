//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/tokens"
	"github.com/elaous/gojiberry/xango"
)

type themeState struct {
	set tokens.Set
	on  bool
}

func (t *themeState) current() tokens.Set {
	if t.set.Colors == nil {
		t.set = tokens.Default()
	}
	return t.set
}

func (u *ui) themeInto(d *docfile.Document) {
	if !u.theme.on {
		d.SetTheme("")
		return
	}
	if css, err := u.theme.set.CSS(); err == nil {
		d.SetTheme(css)
	}
}

func (u *ui) canvasTheme() *canvas.FlowTheme {
	if !u.theme.on || len(u.theme.set.Validate()) > 0 {
		return nil
	}
	c := u.theme.set.Colors
	return &canvas.FlowTheme{Text: c[tokens.Text], Background: c[tokens.Background], Link: c[tokens.Link]}
}

func (u *ui) loadTheme(d docfile.Document) {
	u.theme = themeState{}
	if css := d.Theme(); css != "" {
		if set, ok := tokens.FromCSS(css); ok {
			u.theme = themeState{set: set, on: true}
		}
	}
}

func themePanel(u *ui, state *State, renderCanvas, renderUI func()) xango.Node {
	apply := func() {
		state.Theme = u.canvasTheme()
		renderCanvas()
		renderUI()
	}
	return xango.Frag(components.ThemeEditor(components.ThemeEditorProps{
		Set: u.theme.current(),
		OnChange: func(s tokens.Set) {
			u.theme.set, u.theme.on = s, true
			apply()
		},
		OnReset: func() {
			u.theme = themeState{}
			apply()
		},
	}), u.themeLibrary(func(s tokens.Set) {
		u.theme.set, u.theme.on = s, true
		apply()
	}, renderUI), u.fontSections(renderUI))
}
