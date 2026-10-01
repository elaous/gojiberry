package canvas

// Breaks inserts page breaks, column breaks and horizontal rules into a
// TextFlow:
//
//   - Ctrl/Cmd+Enter is a page break: the text carries on at the top of
//     the next page;
//   - Ctrl/Cmd+Shift+Enter is a column break: it carries on in the next
//     column (or the next page from the last column);
//   - Ctrl/Cmd+Alt+Enter inserts a horizontal rule (like Markdown ---).
//
// The caret ends up after the break, ready to type on the new page. Delete
// in front of a break, or Backspace right after it, removes it. Targets
// that cannot hold breaks (a FloatingText) decline.
type Breaks struct{}

// Command names.
const (
	CmdPageBreak   Command = "break.page"
	CmdColumnBreak Command = "break.column"
	CmdRule        Command = "break.rule"
)

// Name implements Feature.
func (Breaks) Name() string { return "breaks" }

// Install implements Feature.
func (Breaks) Install(r *Registry) {
	r.AddCommand(CmdPageBreak, "Insert page break", "Mod+Enter", func(c *CommandContext) bool { return insertBreak(c.Target, BreakPage) })
	r.AddCommand(CmdColumnBreak, "Insert column break", "Mod+Shift+Enter", func(c *CommandContext) bool { return insertBreak(c.Target, BreakColumn) })
	r.AddCommand(CmdRule, "Insert horizontal rule", "Mod+Alt+Enter", func(c *CommandContext) bool { return insertRule(c.Target) })
}

func insertRule(t EditTarget) bool {
	if !holdsObjects(t) {
		return false
	}
	editCursors(t, func(c Cursor, _ []rune) (int, int, []Item) {
		from, to := c.Range()
		return from, to, []Item{Inline{Object: &Rule{}, Block: true}, Chars{Text: "\n"}}
	})
	return true
}

func insertBreak(t EditTarget, kind BreakKind) bool {
	if !holdsObjects(t) {
		return false
	}
	var edits []Edit
	var out []Cursor
	shift := 0
	for _, c := range normalize(t.Cursors()) {
		from, to := c.Range()
		edits = append(edits, Edit{From: from, To: to, Items: []Item{Break{Kind: kind}}, AfterOps: c.After && from == to})
		out = append(out, Cursor{Anchor: from + shift, Caret: from + shift, After: true})
		shift -= to - from
	}
	t.Replace(edits)
	t.SetCursors(out)
	return true
}
