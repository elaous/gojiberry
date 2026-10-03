package canvas

// FormatEditor is implemented by targets that format characters (a
// TextFlow, a FloatingText).
type FormatEditor interface {
	ApplyFormat(fn func(*Format)) bool
	CommonFormat() Format
}

var (
	_ FormatEditor = (*TextFlow)(nil)
	_ FormatEditor = (*FloatingText)(nil)
)

// TextCase changes how letters are displayed -- UPPER, lower, Title Case,
// small caps -- without touching the text (a Format.Case): Ctrl/Cmd+Alt+U,
// L, T and M. Each toggles back to normal when the selection already has
// it.
type TextCase struct{}

// Command names.
const (
	CmdUpperCase Command = "format.upper-case"
	CmdLowerCase Command = "format.lower-case"
	CmdTitleCase Command = "format.title-case"
	CmdSmallCaps Command = "format.small-caps"
)

// Name implements Feature.
func (TextCase) Name() string { return "text-case" }

// Install implements Feature.
func (TextCase) Install(r *Registry) {
	add := func(name Command, title, chord string, kind CaseKind) {
		r.AddCommand(name, title, chord, func(c *CommandContext) bool {
			fe, ok := c.Target.(FormatEditor)
			if !ok {
				return false
			}
			next := kind
			if fe.CommonFormat().Case == kind {
				next = CaseNormal
			}
			return fe.ApplyFormat(func(f *Format) { f.Case = next })
		})
	}
	add(CmdUpperCase, "UPPER CASE", "Mod+Alt+U", CaseUpper)
	add(CmdLowerCase, "lower case", "Mod+Alt+L", CaseLower)
	add(CmdTitleCase, "Title Case", "Mod+Alt+T", CaseTitle)
	add(CmdSmallCaps, "Small caps", "Mod+Alt+M", CaseSmallCaps)
}

// Links edits the links of a text (Format.Link): Ctrl/Cmd+K asks for an
// address and links the selection to it (with the caret in a link, the
// whole link; with nothing selected and no link, the address itself is
// inserted as one), Ctrl/Cmd+Alt+K removes the link at the caret or in
// the selection, and Ctrl/Cmd+Alt+O opens it.
type Links struct{}

// Command names.
const (
	CmdEditLink   Command = "link.edit"
	CmdRemoveLink Command = "link.remove"
	CmdOpenLink   Command = "link.open"
)

// Name implements Feature.
func (Links) Name() string { return "links" }

// Install implements Feature.
func (Links) Install(r *Registry) {
	r.AddCommand(CmdEditLink, "Edit link", "Mod+K", func(c *CommandContext) bool { return editLink(c) })
	r.AddCommand(CmdRemoveLink, "Remove link", "Mod+Alt+K", func(c *CommandContext) bool { return removeLink(c.Target) })
	r.AddCommand(CmdOpenLink, "Open link", "Mod+Alt+O", func(c *CommandContext) bool {
		url, _, _ := linkAtCursor(c.Target)
		if url == "" || c.OpenLink == nil || !SafeLinkURL(url) {
			return false
		}
		c.OpenLink(url)
		return true
	})
}

// linkAtCursor returns the link at the primary cursor (the first
// selected character's, or the one at or before the caret) and the range
// of the whole link around it.
func linkAtCursor(t EditTarget) (url string, from, to int) {
	cur := t.Cursors()[0]
	n := len([]rune(t.EditText()))
	pos := cur.Caret
	if !cur.Empty() {
		pos, _ = cur.Range()
	}
	links := charLinks(t.Slice(0, n))
	for _, i := range []int{pos, pos - 1} {
		if i >= 0 && i < len(links) && links[i] != "" {
			from, to = i, i+1
			for from > 0 && links[from-1] == links[i] {
				from--
			}
			for to < len(links) && links[to] == links[i] {
				to++
			}
			return links[i], from, to
		}
	}
	return "", 0, 0
}

// charLinks lists the link of every character of items (one entry per
// stream offset; objects have none).
func charLinks(items []Item) []string {
	var out []string
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			for range v.Text {
				out = append(out, v.Format.Link)
			}
		case Inline:
			out = append(out, "")
		}
	}
	return out
}

func editLink(c *CommandContext) bool {
	fe, ok := c.Target.(FormatEditor)
	if !ok || c.Prompt == nil {
		return false
	}
	cur, from, to := linkAtCursor(c.Target)
	url, ok := c.Prompt("Link address (empty removes the link)", cur)
	if !ok {
		return false
	}
	cs := c.Target.Cursors()
	switch {
	case !cs[0].Empty():
	case cur != "":
		cs[0] = Cursor{Anchor: from, Caret: to}
		c.Target.SetCursors(cs)
	case url != "":
		pos := cs[0].Caret
		c.Target.Replace([]Edit{{From: pos, To: pos, Items: []Item{Chars{Text: url, Format: Format{Link: url}}}}})
		c.Target.SetCursors([]Cursor{At(pos + len([]rune(url)))})
		return true
	default:
		return false
	}
	return fe.ApplyFormat(func(f *Format) { f.Link = url })
}

func removeLink(t EditTarget) bool {
	fe, ok := t.(FormatEditor)
	if !ok {
		return false
	}
	cs := t.Cursors()
	if cs[0].Empty() {
		url, from, to := linkAtCursor(t)
		if url == "" {
			return false
		}
		cs[0] = Cursor{Anchor: from, Caret: to}
		t.SetCursors(cs)
	}
	return fe.ApplyFormat(func(f *Format) { f.Link = "" })
}
