package canvas

// MultiCursor adds VS Code-style multiple cursors.
type MultiCursor struct{}

const (
	CmdAddNextMatch     Command = "cursor.add-next-match"
	CmdSelectAllMatches Command = "cursor.select-all-matches"
	CmdCursorsAtEnds    Command = "cursor.line-ends"
	CmdAddCursorAbove   Command = "cursor.add-above"
	CmdAddCursorBelow   Command = "cursor.add-below"
	CmdClearCursors     Command = "cursor.clear"
)

func (MultiCursor) Name() string { return "multi-cursor" }

func (MultiCursor) Install(r *Registry) {
	r.AddCommand(CmdAddNextMatch, "Add selection to next match", "Mod+D", func(c *CommandContext) bool { return addNextMatch(c.Target) })
	r.AddCommand(CmdSelectAllMatches, "Select all matches", "Mod+Shift+L", func(c *CommandContext) bool { return selectAllMatches(c.Target) })
	r.AddCommand(CmdCursorsAtEnds, "Cursors at line ends", "Alt+Shift+I", func(c *CommandContext) bool { return cursorsAtLineEnds(c.Target) })
	r.AddCommand(CmdAddCursorAbove, "Add cursor above", "Mod+Alt+ArrowUp", func(c *CommandContext) bool { return addCursorVertical(c.Target, -1) })
	r.AddCommand(CmdAddCursorBelow, "Add cursor below", "Mod+Alt+ArrowDown", func(c *CommandContext) bool { return addCursorVertical(c.Target, 1) })
	r.AddCommand(CmdClearCursors, "Single cursor", "", func(c *CommandContext) bool {
		cs := c.Target.Cursors()
		if len(cs) < 2 {
			return false
		}
		c.Target.SetCursors(cs[:1])
		return true
	})
}

func wordBounds(rs []rune, o int) (from, to int, ok bool) {
	from, to = o, o
	for from > 0 && isIdentRune(rs[from-1]) {
		from--
	}
	for to < len(rs) && isIdentRune(rs[to]) {
		to++
	}
	return from, to, from < to
}

func findRunes(hay, needle []rune, from int) int {
	for i := max(from, 0); i+len(needle) <= len(hay); i++ {
		match := true
		for j, r := range needle {
			if hay[i+j] != r {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func addNextMatch(t EditTarget) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	last := cs[len(cs)-1]
	if last.Empty() {
		from, to, ok := wordBounds(rs, last.Caret)
		if !ok {
			return false
		}
		cs[len(cs)-1] = Cursor{Anchor: from, Caret: to}
		t.SetCursors(cs)
		return true
	}
	from, to := last.Range()
	needle := rs[from:to]
	taken := map[int]bool{}
	for _, c := range cs {
		f, _ := c.Range()
		taken[f] = true
	}
	var first, after = -1, -1
	for p := 0; ; {
		i := findRunes(rs, needle, p)
		if i < 0 {
			break
		}
		p = i + 1
		if taken[i] {
			continue
		}
		if first < 0 {
			first = i
		}
		if after < 0 && i >= to {
			after = i
		}
	}
	pick := after
	if pick < 0 {
		pick = first
	}
	if pick < 0 {
		return false
	}
	t.SetCursors(append(cs, Cursor{Anchor: pick, Caret: pick + len(needle)}))
	return true
}

func selectAllMatches(t EditTarget) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	last := cs[len(cs)-1]
	from, to := last.Range()
	if last.Empty() {
		var ok bool
		if from, to, ok = wordBounds(rs, last.Caret); !ok {
			return false
		}
	}
	needle := rs[from:to]
	var out []Cursor
	for p := 0; ; {
		i := findRunes(rs, needle, p)
		if i < 0 {
			break
		}
		out = append(out, Cursor{Anchor: i, Caret: i + len(needle)})
		p = i + len(needle)
	}
	t.SetCursors(out)
	return len(out) > 0
}

func cursorsAtLineEnds(t EditTarget) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	var out []Cursor
	for _, c := range cs {
		from, to := c.Range()
		if from == to {
			out = append(out, At(lineEnd(rs, from)))
			continue
		}
		if to > from && rs[to-1] == '\n' {
			to--
		}
		for o := from; ; {
			e := lineEnd(rs, o)
			if e > to {
				break
			}
			out = append(out, At(e))
			if e >= to || e >= len(rs) {
				break
			}
			o = e + 1
		}
	}
	if len(out) == 0 {
		return false
	}
	t.SetCursors(normalize(out))
	return true
}

func addCursorVertical(t EditTarget, dir int) bool {
	rs := textRunes(t)
	cs := t.Cursors()
	pick := cs[0]
	if dir > 0 {
		pick = cs[len(cs)-1]
	}
	pos := pick.Caret
	ls := lineStart(rs, pos)
	col := pos - ls
	var target int
	if dir < 0 {
		if ls == 0 {
			return false
		}
		prevStart := lineStart(rs, ls-1)
		target = prevStart + min(col, ls-1-prevStart)
	} else {
		le := lineEnd(rs, pos)
		if le >= len(rs) {
			return false
		}
		nextEnd := lineEnd(rs, le+1)
		target = le + 1 + min(col, nextEnd-(le+1))
	}
	if dir < 0 {
		t.SetCursors(append([]Cursor{At(target)}, cs...))
	} else {
		t.SetCursors(append(cs, At(target)))
	}
	return true
}
