package gio

import "gioui.org/font"

// IconFont lets Renderer.Layout substitute a Material-Symbols-style icon
// span's literal icon-name text (e.g. iconSpan's own "format_bold", from
// OnDesign's task #107) for a single glyph rendered in Typeface, instead
// of painting the icon name as literal prose. This is the native
// counterpart to the web build's own font ligature substitution: a
// browser's 'liga' GSUB feature turns that same literal text into one
// glyph once the icon webfont has loaded (task #173's own self-hosted
// .material-symbols-outlined rule); Renderer has no ligature-substituting
// shaper of its own to lean on (gioui.org/text.Shaper applies whatever
// GSUB features its own shaping engine defaults to, not an app-chosen
// 'liga' opt-in), so this translates by codepoint instead -- the same
// underlying mechanism the ligature convenience itself is built from:
// every Material Symbols icon lives at one specific Unicode codepoint
// (Google's own distribution publishes a name-to-codepoint "codepoints"
// file alongside the ligature-name convenience), so painting that single
// rune in the icon font produces the identical glyph a browser's own
// ligature substitution would, without needing GSUB support at all.
type IconFont struct {
	// Class is the span's own "class" attribute value this substitution
	// applies to -- xango/gio has no CSS, so there's no other way to tell
	// "this span is an icon, not prose" the way the web build's own
	// class-selector rule does.
	Class string

	// Typeface is the font.Typeface the translated glyph paints with --
	// the caller registers a matching font.FontFace (e.g. via
	// text.WithCollection) under this same name so the glyph actually
	// resolves to the icon font rather than falling through to
	// whatever body-text fallback is otherwise in effect.
	Typeface font.Typeface

	// Translate maps an icon's snake_case name to the single rune that
	// name's glyph lives at in Typeface. A name Translate doesn't
	// recognize (ok == false) renders as plain text instead -- the same
	// graceful "still visible, just not iconified" degradation a
	// browser gives an icon-font span before the webfont has loaded, or
	// for a name outside a vendored subset.
	Translate func(name string) (rune, bool)
}
