package canvas

// Bookmarks names text ranges; Ctrl/Cmd+Alt+B names the selection or character.
type Bookmarks struct{}

const (
	CmdBookmarkAdd    Command = "bookmark.add"
	CmdBookmarkRemove Command = "bookmark.remove"
	CmdBookmarkGoto   Command = "bookmark.goto"
)

func (Bookmarks) Name() string { return "bookmarks" }

func (Bookmarks) Install(r *Registry) {
	r.AddCommand(CmdBookmarkAdd, "Add bookmark", "Mod+Alt+B", func(c *CommandContext) bool {
		if _, ok := c.Target.(BookmarkHost); !ok || c.Prompt == nil {
			return false
		}
		cur := c.Target.Cursors()[0]
		from, to := cur.Range()
		if from == to {
			n := len([]rune(c.Target.EditText()))
			switch {
			case from < n:
				to = from + 1
			case from > 0:
				from--
			default:
				return false
			}
		}
		name, ok := c.Prompt("Bookmark name (link to it with #name)", "")
		if !ok || name == "" {
			return false
		}
		retagAll(c.Target, func(old *Format) {
			if old.Bookmark == name {
				old.Bookmark = ""
			}
		})
		retagRange(c.Target, from, to, func(f *Format) { f.Bookmark = name })
		return true
	})
	r.AddCommand(CmdBookmarkRemove, "Remove bookmark", "", func(c *CommandContext) bool {
		name := bookmarkAtCursor(c.Target)
		if name == "" {
			return false
		}
		retagAll(c.Target, func(f *Format) {
			if f.Bookmark == name {
				f.Bookmark = ""
			}
		})
		return true
	})
	r.AddCommand(CmdBookmarkGoto, "Go to bookmark", "Mod+Alt+G", func(c *CommandContext) bool {
		h, ok := c.Target.(BookmarkHost)
		if !ok || c.Prompt == nil {
			return false
		}
		name, ok := c.Prompt("Go to bookmark", "")
		return ok && h.GotoBookmark(name)
	})
}

// bookmarkAtCursor returns the bookmark at the primary cursor.
func bookmarkAtCursor(t EditTarget) string {
	if _, ok := t.(BookmarkHost); !ok {
		return ""
	}
	cur := t.Cursors()[0]
	pos := cur.Caret
	if !cur.Empty() {
		pos, _ = cur.Range()
	}
	names := charBookmarks(t.Slice(0, len([]rune(t.EditText()))))
	for _, i := range []int{pos, pos - 1} {
		if i >= 0 && i < len(names) && names[i] != "" {
			return names[i]
		}
	}
	return ""
}

// retagRange rewrites the format of [from, to) with fn.
func retagRange(t EditTarget, from, to int, fn func(*Format)) {
	items := t.Slice(from, to)
	out := make([]Item, len(items))
	for i, it := range items {
		if ch, ok := it.(Chars); ok {
			fn(&ch.Format)
			it = ch
		}
		out[i] = it
	}
	cs := t.Cursors()
	t.Replace([]Edit{{From: from, To: to, Items: out}})
	t.SetCursors(cs)
}

// retagAll applies fn to every character's format.
func retagAll(t EditTarget, fn func(*Format)) {
	n := len([]rune(t.EditText()))
	fms := charFormats(t.Slice(0, n))
	for i := 0; i < len(fms); {
		changed := func(j int) bool { f := fms[j]; fn(&f); return f != fms[j] }
		if !changed(i) {
			i++
			continue
		}
		j := i
		for j < len(fms) && changed(j) {
			j++
		}
		retagRange(t, i, j, fn)
		i = j
	}
}
