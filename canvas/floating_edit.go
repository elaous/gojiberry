package canvas

import (
	"slices"
	"strings"

	"github.com/elaous/gojiberry/complete"
)

// --- auto-closing pairs ------------------------------------------------

// DefaultAutoClose is the code-editor set: " ' ` ( [ {.
func DefaultAutoClose() map[rune]rune {
	return map[rune]rune{'(': ')', '[': ']', '{': '}', '"': '"', '\'': '\'', '`': '`'}
}

func (ft *FloatingText) isCloser(r rune) bool {
	for _, c := range ft.AutoClose {
		if c == r {
			return true
		}
	}
	return false
}

// betweenPair reports whether the caret sits between an opener and its
// closer, with nothing selected.
func (ft *FloatingText) betweenPair() bool {
	if ft.caret != ft.anchor || ft.caret == 0 || ft.caret >= len(ft.runes) {
		return false
	}
	closer, ok := ft.AutoClose[ft.runes[ft.caret-1].r]
	return ok && closer == ft.runes[ft.caret].r
}

// typeRune handles one typed character with auto-closing: it wraps a
// selection in a pair, overtypes a closer already ahead of the caret, or
// inserts a pair -- except where that would be annoying: a quote after a
// letter (an apostrophe) or an opener in front of a word.
func (ft *FloatingText) typeRune(r rune) {
	closer, isOpener := ft.AutoClose[r]
	from, to := ft.Selection()
	f := ft.typingFormat()
	if isOpener && from != to {
		inner := slices.Clone(ft.runes[from:to])
		ins := append(append([]fRune{{r, f}}, inner...), fRune{closer, f})
		ft.replace(from, to, ins)
		ft.anchor, ft.caret = from+1, from+1+len(inner) // keep the wrapped text selected
		return
	}
	if from == to {
		var prev, next rune
		if ft.caret > 0 {
			prev = ft.runes[ft.caret-1].r
		}
		if ft.caret < len(ft.runes) {
			next = ft.runes[ft.caret].r
		}
		if ft.isCloser(r) && next == r {
			ft.caret++
			ft.anchor = ft.caret
			return
		}
		symmetric := closer == r
		quoteAfterWord := symmetric && (isWordRune(prev) || prev == r)
		if isOpener && !quoteAfterWord && !isWordRune(next) {
			ft.replace(from, to, []fRune{{r, f}, {closer, f}})
			ft.caret, ft.anchor = from+1, from+1
			return
		}
	}
	ft.replace(from, to, []fRune{{r, f}})
}

// InsertText types s at the caret, replacing any selection (edit mode,
// while editing). A single typed character gets auto-closing and
// completion; longer text (a paste) is inserted as is. Carriage returns
// are dropped.
func (ft *FloatingText) InsertText(s string) bool {
	s = strings.ReplaceAll(s, "\r", "")
	if ft.mode != ModeEdit || !ft.editing || s == "" {
		return false
	}
	if len(ft.extra) > 0 {
		ft.multiInsert(s)
		return true
	}
	rs := []rune(s)
	if len(rs) != 1 || rs[0] == '\n' {
		ok := ft.insertPlain(s)
		ft.closeSuggestions()
		return ok
	}
	if ft.anchor == ft.caret {
		ft.histKind = "type"
	}
	ft.typeRune(rs[0])
	ft.pending = Format{}
	ft.caretOn = true
	ft.changed()
	ft.updateSuggestions()
	return true
}

// PasteText inserts clipboard text exactly as given (no auto-closing).
func (ft *FloatingText) PasteText(text string) bool {
	if len(ft.extra) > 0 && ft.mode == ModeEdit && ft.editing {
		ft.multiPaste(text)
		return true
	}
	ok := ft.insertPlain(text)
	ft.closeSuggestions()
	return ok
}

// --- completion --------------------------------------------------------

// Completer suggests completions for a partly typed word.
type Completer interface {
	// Complete returns candidate whole words for the prefix, best first.
	Complete(prefix string) []string
}

// CompleterFunc adapts a function to Completer.
type CompleterFunc func(prefix string) []string

// Complete implements Completer.
func (f CompleterFunc) Complete(prefix string) []string { return f(prefix) }

const (
	minCompletePrefix = 2
	maxSuggestions    = 6
)

func isIdentRune(r rune) bool { return isWordRune(r) || r == '_' }

// wordBeforeCaret returns the identifier prefix ending at the caret and
// where it starts.
func (ft *FloatingText) wordBeforeCaret() (string, int) {
	start := ft.caret
	for start > 0 && isIdentRune(ft.runes[start-1].r) {
		start--
	}
	var b strings.Builder
	for _, fr := range ft.runes[start:ft.caret] {
		b.WriteRune(fr.r)
	}
	return b.String(), start
}

// defaultComplete suggests the longer words in the text and Vocabulary
// that start with prefix (see completeWords).
func (ft *FloatingText) defaultComplete(prefix string) []string {
	rs := make([]rune, len(ft.runes))
	for i, fr := range ft.runes {
		rs[i] = fr.r
	}
	return completeWords(string(rs), ft.Vocabulary, prefix)
}

// completeWords is the one ranking behind every completion popup in the
// canvas: package complete's index of the text's own words (most used and
// most recent first) and the vocabulary, capped at maxSuggestions.
func completeWords(text string, vocabulary []string, prefix string) []string {
	var ix complete.Index
	ix.AddText(text)
	ix.AddVocabulary(vocabulary, 1)
	var out []string
	for _, s := range ix.Suggest(prefix, complete.Options{Max: maxSuggestions, MinPrefix: minCompletePrefix}) {
		out = append(out, s.Word)
	}
	return out
}

func (ft *FloatingText) suggestMenu() *Menu {
	if ft.sugg == nil {
		ft.sugg = NewMenu(ft.Painter)
		ft.sugg.Style.MinWidth = 120
	}
	ft.sugg.Painter = ft.Painter
	return ft.sugg
}

func (ft *FloatingText) suggestionsOpen() bool { return ft.sugg != nil && ft.sugg.IsOpen() }

func (ft *FloatingText) closeSuggestions() {
	if ft.sugg != nil {
		ft.sugg.Close()
	}
}

// updateSuggestions recomputes the completion popup for the word before
// the caret.
func (ft *FloatingText) updateSuggestions() {
	ft.closeSuggestions()
	if ft.mode != ModeEdit || !ft.editing || ft.caret != ft.anchor || ft.Painter == nil {
		return
	}
	prefix, start := ft.wordBeforeCaret()
	if len([]rune(prefix)) < minCompletePrefix {
		return
	}
	var words []string
	if ft.Completer != nil {
		words = ft.Completer.Complete(prefix)
	} else {
		words = ft.defaultComplete(prefix)
	}
	if len(words) == 0 {
		return
	}
	ft.sync()
	x, y, h, ok := ft.inner.CaretRect(ft.caret)
	if !ok {
		return
	}
	items := make([]MenuItem, len(words))
	for i, w := range words {
		w := w
		items[i] = MenuItem{Label: w, Action: func() { ft.acceptSuggestion(w) }}
	}
	ft.suggFrom = start
	m := ft.suggestMenu()
	m.OpenWithin(items, x, y+h+2, ft.viewport)
	m.SetHover(0)
}

// acceptSuggestion replaces the word being typed with the completion.
func (ft *FloatingText) acceptSuggestion(word string) {
	f := ft.typingFormat()
	ins := make([]fRune, 0, len(word))
	for _, r := range word {
		ins = append(ins, fRune{r, f})
	}
	ft.replace(ft.suggFrom, ft.caret, ins)
	ft.pending = Format{}
	ft.caretOn = true
	ft.changed()
}

// Suggestions returns the completions currently offered.
func (ft *FloatingText) Suggestions() []string {
	if !ft.suggestionsOpen() {
		return nil
	}
	return ft.sugg.Labels()
}

// --- color palette ------------------------------------------------------

var (
	textColors = []struct{ name, css string }{
		{"Black", "#000000"}, {"Gray", "#5f6368"}, {"Red", "#d93025"}, {"Orange", "#e8710a"},
		{"Green", "#188038"}, {"Blue", "#1a73e8"}, {"Purple", "#9334e6"},
	}
	highlightColors = []struct{ name, css string }{
		{"Yellow", "#fff475"}, {"Green", "#ccff90"}, {"Cyan", "#a7ffeb"}, {"Pink", "#fdcfe8"}, {"Orange", "#fbbc04"}, {"Gray", "#e8eaed"},
	}
)

func (ft *FloatingText) palMenu() *Menu {
	if ft.pal == nil {
		ft.pal = NewMenu(ft.Painter)
		ft.pal.Style.MinWidth = 130
	}
	ft.pal.Painter = ft.Painter
	return ft.pal
}

func (ft *FloatingText) palOpen() bool { return ft.pal != nil && ft.pal.IsOpen() }

// openPalette shows the color menu under toolbar button i.
func (ft *FloatingText) openPalette(i int, kind string) {
	ft.closeSuggestions()
	if ft.palOpen() && ft.palKind == kind { // clicking the button again closes it
		ft.pal.Close()
		return
	}
	ft.palKind = kind
	var items []MenuItem
	if kind == "text" {
		for _, c := range textColors {
			c := c
			items = append(items, MenuItem{Label: c.name, Swatch: c.css, Action: func() { ft.SetTextColor(c.css) }})
		}
		items = append(items, MenuItem{Separator: true}, MenuItem{Label: "Default color", Action: func() { ft.SetTextColor("") }})
	} else {
		for _, c := range highlightColors {
			c := c
			items = append(items, MenuItem{Label: c.name, Swatch: c.css, Action: func() { ft.SetHighlight(c.css) }})
		}
		items = append(items, MenuItem{Separator: true}, MenuItem{Label: "No highlight", Action: func() { ft.SetHighlight("") }})
	}
	b := ft.buttonRect(i)
	ft.palMenu().OpenWithin(items, b[0], b[1]+b[3]+2, ft.viewport)
}

// SetTextColor colors the selected text (or the text typed next); ""
// returns it to the default color.
func (ft *FloatingText) SetTextColor(css string) bool {
	return ft.ApplyFormat(func(f *Format) { f.Color = css })
}

// SetHighlight highlights the selected text (or the text typed next); ""
// removes the highlight.
func (ft *FloatingText) SetHighlight(css string) bool {
	return ft.ApplyFormat(func(f *Format) { f.Background = css })
}

// ToggleUnderline, ToggleStrike, ToggleSuperscript and ToggleSubscript
// switch a decoration on the selection (or the text typed next): on
// unless it is on everywhere already. Superscript and subscript exclude
// each other.
func (ft *FloatingText) ToggleUnderline() bool {
	return ft.toggleDecoration(func(s TextStyle) bool { return s.Underline }, func(f *Format, on bool) { f.Underline = on })
}

func (ft *FloatingText) ToggleStrike() bool {
	return ft.toggleDecoration(func(s TextStyle) bool { return s.Strike }, func(f *Format, on bool) { f.Strike = on })
}

func (ft *FloatingText) ToggleSuperscript() bool {
	return ft.toggleDecoration(func(s TextStyle) bool { return s.Superscript }, func(f *Format, on bool) {
		f.Superscript = on
		if on {
			f.Subscript = false
		}
	})
}

func (ft *FloatingText) ToggleSubscript() bool {
	return ft.toggleDecoration(func(s TextStyle) bool { return s.Subscript }, func(f *Format, on bool) {
		f.Subscript = on
		if on {
			f.Superscript = false
		}
	})
}

func (ft *FloatingText) toggleDecoration(has func(TextStyle) bool, set func(*Format, bool)) bool {
	all := true
	for _, s := range ft.current() {
		all = all && has(s)
	}
	return ft.ApplyFormat(func(f *Format) { set(f, !all) })
}

// --- popups: mouse, keys, overlay ---------------------------------------

func (ft *FloatingText) closePopups() {
	ft.closeSuggestions()
	if ft.pal != nil {
		ft.pal.Close()
	}
}

// popupsMouseDown lets the open palette or completion popup take a press
// on one of its items; a press elsewhere closes them (and carries on).
func (ft *FloatingText) popupsMouseDown(x, y float64) bool {
	if ft.palOpen() {
		// The button that opened the palette closes it again.
		if w := ft.whatAt(x, y); (w == "button:6" && ft.palKind == "text") || (w == "button:7" && ft.palKind == "highlight") {
			ft.pal.Close()
			return true
		}
	}
	if ft.palOpen() && ft.pal.MouseDown(x, y) {
		return true
	}
	if ft.suggestionsOpen() && ft.sugg.MouseDown(x, y) {
		return true
	}
	return false
}

// HandleKey handles editing keys while editing, and drives the color
// palette and the completion popup: while the palette is open it gets
// every key; while completions are showing, Up/Down choose, Tab or Enter
// accepts and Escape dismisses, and any other key edits as usual.
func (ft *FloatingText) HandleKey(key string, shift bool) bool {
	if ft.mode != ModeEdit {
		return false
	}
	if ft.palOpen() {
		return ft.pal.HandleKey(key)
	}
	if !ft.editing {
		return false
	}
	if ft.suggestionsOpen() {
		switch key {
		case "ArrowDown", "ArrowUp":
			m := ft.sugg
			n := len(m.items)
			if key == "ArrowDown" {
				m.SetHover((m.Hover() + 1) % n)
			} else {
				m.SetHover((m.Hover() - 1 + n) % n)
			}
			return true
		case "Tab", "Enter":
			ft.sugg.Activate(ft.sugg.Hover())
			return true
		case "Escape":
			ft.closeSuggestions()
			return true
		}
	}
	handled := ft.handleEditKey(key, shift)
	switch key {
	case "Backspace", "Delete":
		ft.updateSuggestions()
	default:
		ft.closeSuggestions()
	}
	return handled
}

// SetViewport implements ViewportSetter.
func (ft *FloatingText) SetViewport(x, y, w, h float64) { ft.viewport = [4]float64{x, y, w, h} }

// DrawOverlay implements Overlayer: the palette and completion popups.
func (ft *FloatingText) DrawOverlay() {
	if ft.mode != ModeEdit {
		return
	}
	if ft.pal != nil {
		ft.pal.Draw()
	}
	if ft.sugg != nil {
		ft.sugg.Draw()
	}
}

// --- selection, formats, commands, clipboard -----------------------------

// SetFormatClipboard implements FormatClipboardSetter.
func (ft *FloatingText) SetFormatClipboard(c *FormatClipboard) { ft.formats = c }

// SelectAll selects all the text.
func (ft *FloatingText) SelectAll() {
	ft.anchor, ft.caret = 0, len(ft.runes)
}

// SelectedText returns the selected text.
func (ft *FloatingText) SelectedText() string {
	from, to := ft.Selection()
	var b strings.Builder
	for _, fr := range ft.runes[from:to] {
		b.WriteRune(fr.r)
	}
	return b.String()
}

// CommonFormat returns the formatting shared by the whole selection --
// where it varies a field is left unset (the minimum common formatting);
// with no selection it is the format at the caret.
func (ft *FloatingText) CommonFormat() Format {
	from, to := ft.Selection()
	if from == to {
		return ft.typingFormat()
	}
	fs := make([]Format, 0, to-from)
	for _, fr := range ft.runes[from:to] {
		fs = append(fs, fr.f)
	}
	return CommonFormat(fs...)
}

// PasteFormat gives the selected text exactly format f (replacing its
// formatting); with no selection f applies to the text typed next.
func (ft *FloatingText) PasteFormat(f Format) {
	from, to := ft.Selection()
	if from == to {
		ft.pending = f
		return
	}
	for i := from; i < to; i++ {
		ft.runes[i].f = f
	}
	ft.changed()
}

// HandleCommand implements Commander for the editing commands in the
// Keymap.
func (ft *FloatingText) HandleCommand(cmd Command) bool {
	if ft.mode != ModeEdit || !ft.editing {
		return false
	}
	switch cmd {
	case CmdSelectAll:
		ft.SelectAll()
	case CmdBold:
		ft.ToggleBold()
	case CmdItalic:
		ft.ToggleItalic()
	case CmdUnderline:
		ft.ToggleUnderline()
	case CmdStrike:
		ft.ToggleStrike()
	case CmdSuperscript:
		ft.ToggleSuperscript()
	case CmdSubscript:
		ft.ToggleSubscript()
	case CmdCopyFormat:
		if ft.formats == nil {
			return false
		}
		ft.formats.Set(ft.CommonFormat())
	case CmdPasteFormat:
		if ft.formats == nil {
			return false
		}
		f, ok := ft.formats.Get()
		if !ok {
			return false
		}
		ft.PasteFormat(f)
	default:
		return false
	}
	ft.closeSuggestions()
	ft.caretOn = true
	return true
}

// CopyText implements Copier.
func (ft *FloatingText) CopyText() (string, bool) {
	if ft.mode != ModeEdit || !ft.editing {
		return "", false
	}
	if len(ft.extra) > 0 {
		return ft.multiSelectedText()
	}
	if from, to := ft.Selection(); from == to {
		return "", false
	}
	return ft.SelectedText(), true
}

// CutText implements Cutter.
func (ft *FloatingText) CutText() (string, bool) {
	text, ok := ft.CopyText()
	if !ok {
		return "", false
	}
	if len(ft.extra) > 0 {
		ft.multiInsert("")
		return text, true
	}
	from, to := ft.Selection()
	ft.replace(from, to, nil)
	ft.changed()
	ft.closeSuggestions()
	return text, true
}

var (
	_ Commander             = (*FloatingText)(nil)
	_ Copier                = (*FloatingText)(nil)
	_ Cutter                = (*FloatingText)(nil)
	_ Paster                = (*FloatingText)(nil)
	_ FormatClipboardSetter = (*FloatingText)(nil)
	_ Overlayer             = (*FloatingText)(nil)
	_ ViewportSetter        = (*FloatingText)(nil)
)

// PaletteMenu returns the color palette's menu (open while it is showing).
func (ft *FloatingText) PaletteMenu() *Menu { return ft.palMenu() }

// SuggestionMenu returns the completion popup's menu (open while
// suggestions are showing).
func (ft *FloatingText) SuggestionMenu() *Menu { return ft.suggestMenu() }

// ToolbarButtonRect returns toolbar button i's rectangle (x, y, w, h):
// 0 Move, 1 B, 2 I, 3 A-, 4 A+, 5 Wrap, 6 Color, 7 Mark.
func (ft *FloatingText) ToolbarButtonRect(i int) (x, y, w, h float64) {
	ft.resolve()
	r := ft.buttonRect(i)
	return r[0], r[1], r[2], r[3]
}
