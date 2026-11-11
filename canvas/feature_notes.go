package canvas

import "strings"

// FootnoteHost is implemented by targets with footnotes (a TextFlow).
type FootnoteHost interface {
	// FootnoteSet returns the notes, making the set if there is none.
	FootnoteSet() *FootnoteSet
}

var _ FootnoteHost = (*TextFlow)(nil)

// FootnoteSet implements FootnoteHost.
func (f *TextFlow) FootnoteSet() *FootnoteSet {
	if f.Footnotes == nil {
		f.Footnotes = NewFootnoteSet()
	}
	return f.Footnotes
}

// Notes adds footnotes, a table of contents and an index.
type Notes struct{}

const (
	CmdFootnoteAdd    Command = "footnote.add"
	CmdFootnoteEdit   Command = "footnote.edit"
	CmdFootnoteDelete Command = "footnote.delete"
	CmdTOCInsert      Command = "toc.insert"
	CmdIndexMark      Command = "index.mark"
	CmdIndexUnmark    Command = "index.unmark"
	CmdIndexInsert    Command = "index.insert"
)

func (Notes) Name() string { return "notes" }

// footnoteAtCursor finds the footnote mark at or before the caret.
func footnoteAtCursor(t EditTarget) (FootnoteHost, *FootnoteRef, int) {
	h, ok := t.(FootnoteHost)
	if !ok {
		return nil, nil, 0
	}
	cur := t.Cursors()[0]
	pos := cur.Caret
	if !cur.Empty() {
		pos, _ = cur.Range()
	}
	for _, at := range []int{pos - 1, pos} {
		if at < 0 || at >= len([]rune(t.EditText())) {
			continue
		}
		for _, it := range t.Slice(at, at+1) {
			if in, ok := it.(Inline); ok {
				if ref, ok := in.Object.(*FootnoteRef); ok {
					return h, ref, at
				}
			}
		}
	}
	return nil, nil, 0
}

// insertObject puts an inline object at the end of the selection.
func insertObject(t EditTarget, in Inline) {
	cur := t.Cursors()[0]
	_, at := cur.Range()
	items := []Item{in}
	if in.Block {
		items = append(items, Chars{Text: "\n"})
	}
	t.Replace([]Edit{{From: at, To: at, Items: items}})
	t.SetCursors([]Cursor{At(at + len(items))})
}

func (Notes) Install(r *Registry) {
	r.AddCommand(CmdFootnoteAdd, "Add footnote", "Mod+Alt+F", func(c *CommandContext) bool {
		h, ok := c.Target.(FootnoteHost)
		if !ok || (c.Prompt == nil && c.EditNote == nil) {
			return false
		}
		if c.EditNote != nil {
			target, at := c.Target, c.Target.Cursors()[0]
			c.EditNote("Footnote", "", MaxFootnoteRunes, func(text string, ok bool) {
				if !ok || strings.TrimSpace(text) == "" {
					return
				}
				target.SetCursors([]Cursor{at})
				n := h.FootnoteSet().Add(Footnote{Text: text})
				insertObject(target, Inline{Object: &FootnoteRef{ID: n.ID}})
				if c.Redraw != nil {
					c.Redraw()
				}
			})
			return true
		}
		text, ok := c.Prompt("Footnote", "")
		if !ok || strings.TrimSpace(text) == "" {
			return false
		}
		n := h.FootnoteSet().Add(Footnote{Text: text})
		insertObject(c.Target, Inline{Object: &FootnoteRef{ID: n.ID}})
		return true
	})
	r.AddCommand(CmdFootnoteEdit, "Edit footnote", "Mod+Alt+Shift+F", func(c *CommandContext) bool {
		h, ref, _ := footnoteAtCursor(c.Target)
		if h == nil || (c.Prompt == nil && c.EditNote == nil) {
			return false
		}
		n, _ := h.FootnoteSet().Get(ref.ID)
		if c.EditNote != nil {
			target, id := c.Target, ref.ID
			c.EditNote("Footnote", n.Text, MaxFootnoteRunes, func(text string, ok bool) {
				if !ok || strings.TrimSpace(text) == "" {
					return
				}
				h.FootnoteSet().Add(Footnote{ID: id, Text: text})
				touch(target)
				if c.Redraw != nil {
					c.Redraw()
				}
			})
			return true
		}
		text, ok := c.Prompt("Footnote", n.Text)
		if !ok || strings.TrimSpace(text) == "" {
			return false
		}
		h.FootnoteSet().Add(Footnote{ID: ref.ID, Text: text})
		touch(c.Target)
		return true
	})
	r.AddCommand(CmdFootnoteDelete, "Delete footnote", "", func(c *CommandContext) bool {
		h, ref, at := footnoteAtCursor(c.Target)
		if h == nil {
			return false
		}
		h.FootnoteSet().Delete(ref.ID)
		cs := c.Target.Cursors()
		c.Target.Replace([]Edit{{From: at, To: at + 1}})
		c.Target.SetCursors(cs)
		return true
	})
	r.AddCommand(CmdTOCInsert, "Insert table of contents", "", func(c *CommandContext) bool {
		if _, ok := c.Target.(interface{ Outline() []OutlineEntry }); !ok {
			return false
		}
		insertObject(c.Target, Inline{Object: &TOCObject{}, Block: true})
		return true
	})
	r.AddCommand(CmdIndexMark, "Mark index entry", "Mod+Alt+X", func(c *CommandContext) bool {
		if _, ok := c.Target.(interface{ IndexEntries() []IndexEntry }); !ok || c.Prompt == nil {
			return false
		}
		cur := c.Target.Cursors()[0]
		if cur.Empty() {
			return false
		}
		from, to := cur.Range()
		def := ""
		for _, it := range c.Target.Slice(from, to) {
			if ch, ok := it.(Chars); ok {
				def += ch.Text
			}
		}
		term, ok := c.Prompt("Index term", strings.TrimSpace(strings.ReplaceAll(def, "\n", " ")))
		if !ok || strings.TrimSpace(term) == "" {
			return false
		}
		retagRange(c.Target, from, to, func(f *Format) { f.Index = strings.TrimSpace(term) })
		return true
	})
	r.AddCommand(CmdIndexUnmark, "Unmark index entry", "", func(c *CommandContext) bool {
		if _, ok := c.Target.(interface{ IndexEntries() []IndexEntry }); !ok {
			return false
		}
		cur := c.Target.Cursors()[0]
		pos := cur.Caret
		if !cur.Empty() {
			pos, _ = cur.Range()
		}
		terms := charIndexTerms(c.Target.Slice(0, len([]rune(c.Target.EditText()))))
		for _, i := range []int{pos, pos - 1} {
			if i >= 0 && i < len(terms) && terms[i] != "" {
				term := terms[i]
				retagAll(c.Target, func(f *Format) {
					if f.Index == term {
						f.Index = ""
					}
				})
				return true
			}
		}
		return false
	})
	r.AddCommand(CmdIndexInsert, "Insert index", "", func(c *CommandContext) bool {
		if _, ok := c.Target.(interface{ IndexEntries() []IndexEntry }); !ok {
			return false
		}
		insertObject(c.Target, Inline{Object: &IndexObject{}, Block: true})
		return true
	})
}
