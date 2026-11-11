package gio

import "gioui.org/font"

type IconFont struct {
	Class string

	Typeface font.Typeface

	Translate func(name string) (rune, bool)
}
