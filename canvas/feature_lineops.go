package canvas

import "slices"

// LineOps are the line editing commands of VS Code, on logical lines
// (separated by '\n') and on every cursor at once:
//
//   - Alt+Up / Alt+Down move the line (or every line the selection
//     touches) up or down;
//   - Alt+Shift+Up / Alt+Shift+Down copy it above or below (the
//     cursors go with the copy for "down" and stay on the upper copy
//     for "up");
//   - Ctrl/Cmd+Shift+K deletes it.
//
// Formatting travels with the text.
type LineOps struct{}

// Command names.
const (
	CmdMoveLineUp        Command = "line.move-up"
	CmdMoveLineDown      Command = "line.move-down"
	CmdDuplicateLineUp   Command = "line.duplicate-up"
	CmdDuplicateLineDown Command = "line.duplicate-down"
	CmdDeleteLine        Command = "line.delete"
)

// Name implements Feature.
func (LineOps) Name() string { return "line-ops" }

// Install implements Feature.
func (LineOps) Install(r *Registry) {
	r.AddCommand(CmdMoveLineUp, "Move line up", "Alt+ArrowUp", func(c *CommandContext) bool { return moveLines(c.Target, -1) })
	r.AddCommand(CmdMoveLineDown, "Move line down", "Alt+ArrowDown", func(c *CommandContext) bool { return moveLines(c.Target, 1) })
	r.AddCommand(CmdDuplicateLineUp, "Copy line up", "Alt+Shift+ArrowUp", func(c *CommandContext) bool { return duplicateLines(c.Target, false) })
	r.AddCommand(CmdDuplicateLineDown, "Copy line down", "Alt+Shift+ArrowDown", func(c *CommandContext) bool { return duplicateLines(c.Target, true) })
	r.AddCommand(CmdDeleteLine, "Delete line", "Mod+Shift+K", func(c *CommandContext) bool { return deleteLines(c.Target) })
}

// lineBlock is a run of whole lines: a is the start of the first, b the
// end (before its '\n') of the last.
type lineBlock struct{ a, b int }

func (l lineBlock) has(o int) bool { return o >= l.a && o <= l.b }

// blocksOf returns the lines the cursors touch, merged where they
// overlap or touch. A selection that ends at the very start of a line
// does not include that line.
func blocksOf(rs []rune, cs []Cursor) []lineBlock {
	var bs []lineBlock
	for _, c := range cs {
		from, to := c.Range()
		if to > from && to <= len(rs) && rs[to-1] == '\n' {
			to--
		}
		bs = append(bs, lineBlock{lineStart(rs, from), lineEnd(rs, to)})
	}
	slices.SortFunc(bs, func(x, y lineBlock) int { return x.a - y.a })
	var out []lineBlock
	for _, b := range bs {
		if n := len(out); n > 0 && b.a <= out[n-1].b+1 {
			out[n-1].b = max(out[n-1].b, b.b)
			continue
		}
		out = append(out, b)
	}
	return out
}

var newline = []Item{Chars{Text: "\n"}}

func join(parts ...[]Item) []Item {
	var out []Item
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// mapCursors moves every cursor end with fn.
func mapCursors(cs []Cursor, fn func(o int) int) []Cursor {
	out := make([]Cursor, len(cs))
	for i, c := range cs {
		out[i] = Cursor{Anchor: fn(c.Anchor), Caret: fn(c.Caret)}
	}
	return out
}

func moveLines(t EditTarget, dir int) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	type move struct {
		b     lineBlock
		shift int
	}
	var edits []Edit
	var moves []move
	for _, b := range blocksOf(rs, cs) {
		if dir < 0 {
			if b.a == 0 {
				continue
			}
			ps := lineStart(rs, b.a-1)
			edits = append(edits, Edit{From: ps, To: b.b, Items: join(t.Slice(b.a, b.b), newline, t.Slice(ps, b.a-1))})
			moves = append(moves, move{b, -(b.a - ps)})
		} else {
			if b.b >= len(rs) {
				continue
			}
			ne := lineEnd(rs, b.b+1)
			edits = append(edits, Edit{From: b.a, To: ne, Items: join(t.Slice(b.b+1, ne), newline, t.Slice(b.a, b.b))})
			moves = append(moves, move{b, ne - b.b})
		}
	}
	if len(edits) == 0 {
		return false
	}
	t.Replace(edits)
	t.SetCursors(mapCursors(cs, func(o int) int {
		for _, m := range moves {
			if m.b.has(o) {
				return o + m.shift
			}
		}
		return o
	}))
	return true
}

func duplicateLines(t EditTarget, down bool) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	bs := blocksOf(rs, cs)
	var edits []Edit
	for _, b := range bs {
		blk := t.Slice(b.a, b.b)
		if down {
			edits = append(edits, Edit{From: b.b, To: b.b, Items: join(newline, blk)})
		} else {
			edits = append(edits, Edit{From: b.a, To: b.a, Items: join(blk, newline)})
		}
	}
	t.Replace(edits)
	t.SetCursors(mapCursors(cs, func(o int) int {
		prior := 0
		for _, b := range bs {
			ins := b.b - b.a + 1
			if b.has(o) {
				if down {
					prior += ins
				}
				return o + prior
			}
			prior += ins
		}
		return o
	}))
	return true
}

func deleteLines(t EditTarget) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	bs := blocksOf(rs, cs)
	var edits []Edit
	type gone struct{ from, to int }
	var gs []gone
	for _, b := range bs {
		from, to := b.a, b.b
		switch {
		case to < len(rs): // take the line's own newline
			to++
		case from > 0: // last line: take the newline before it
			from--
		}
		edits = append(edits, Edit{From: from, To: to})
		gs = append(gs, gone{from, to})
	}
	t.Replace(edits)
	var out []Cursor
	prior := 0
	for _, g := range gs {
		out = append(out, At(g.from-prior))
		prior += g.to - g.from
	}
	t.SetCursors(out)
	return true
}
