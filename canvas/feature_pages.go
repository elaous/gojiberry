package canvas

// PageEditor is implemented by a target that knows the sheets it flows
// into and where the caret is on them (a TextFlow with Pages).
type PageEditor interface {
	// PageSet returns the sheets, or nil.
	PageSet() *PageSet
	// CaretPage is the sheet the primary cursor is on.
	CaretPage() int
}

var _ PageEditor = (*TextFlow)(nil)

// PageSet implements PageEditor.
func (f *TextFlow) PageSet() *PageSet { return f.Pages }

// CaretPage implements PageEditor.
func (f *TextFlow) CaretPage() int {
	if ln, ok := f.lineAt(f.sel.caret); ok {
		return ln.Page
	}
	return 0
}

// PageFurniture is the page-level commands for a TextFlow that flows into
// a PageSet: removing the header, footer, page number, watermark or
// background picture from one page, from this page on, or from all pages
// (the "none" choice; the caret's page is the one meant); setting a
// watermark from this page on; and the two-page spread view.
type PageFurniture struct{}

// Name implements Feature.
func (PageFurniture) Name() string { return "page-furniture" }

// Command names are built as "page.<what>.remove-<scope>": for example
// CmdPageRemove("header", RemoveForward).
const (
	CmdSetWatermark Command = "page.watermark.set"
	CmdSpread       Command = "page.spread"
	CmdSpreadBook   Command = "page.spread-book"
)

// CmdPageRemove names the command removing what ("header", "footer",
// "number", "watermark", "background") with the given scope.
func CmdPageRemove(what string, scope RemoveScope) Command {
	return Command("page." + what + ".remove-" + [...]string{"page", "forward", "all"}[scope])
}

// Install implements Feature.
func (PageFurniture) Install(r *Registry) {
	pages := func(c *CommandContext) (*PageSet, int, bool) {
		pe, ok := c.Target.(PageEditor)
		if !ok || pe.PageSet() == nil || len(pe.PageSet().Pages) == 0 {
			return nil, 0, false
		}
		return pe.PageSet(), pe.CaretPage(), true
	}
	things := []struct {
		what, title string
		remove      func(*PageSet, int, RemoveScope)
	}{
		{"header", "header", (*PageSet).RemoveHeader},
		{"footer", "footer", (*PageSet).RemoveFooter},
		{"number", "page number", (*PageSet).RemovePageNumber},
		{"watermark", "watermark", (*PageSet).RemoveWatermark},
		{"background", "background picture", (*PageSet).RemoveBackground},
	}
	scopes := []struct {
		scope RemoveScope
		title string
	}{{RemoveThisPage, "from this page only"}, {RemoveForward, "from this page on"}, {RemoveAll, "from all pages"}}
	for _, th := range things {
		for _, sc := range scopes {
			th, sc := th, sc
			r.AddCommand(CmdPageRemove(th.what, sc.scope), "Remove "+th.title+" "+sc.title, "", func(c *CommandContext) bool {
				ps, page, ok := pages(c)
				if !ok {
					return false
				}
				th.remove(ps, page, sc.scope)
				return true
			})
		}
	}
	r.AddCommand(CmdSetWatermark, "Set watermark text from this page on", "", func(c *CommandContext) bool {
		ps, page, ok := pages(c)
		if !ok || c.Prompt == nil {
			return false
		}
		cur := ""
		if w := ps.ResolvedWatermark(page); w != nil {
			cur = w.Text
		}
		text, ok := c.Prompt("Watermark text (empty removes it)", cur)
		if !ok {
			return false
		}
		if text == "" {
			ps.RemoveWatermark(page, RemoveForward)
			return true
		}
		wm := Watermark{Text: text}
		if w := ps.ResolvedWatermark(page); w != nil {
			wm = *w
			wm.Text = text
		}
		ps.Pages[page].Watermark = &wm
		ps.changed()
		return true
	})
	r.AddCommand(CmdSpread, "Two-page spread view", "", func(c *CommandContext) bool {
		ps, _, ok := pages(c)
		if !ok {
			return false
		}
		ps.Spread = !ps.Spread
		ps.changed()
		return true
	})
	r.AddCommand(CmdSpreadBook, "Book spread (first page alone)", "", func(c *CommandContext) bool {
		ps, _, ok := pages(c)
		if !ok {
			return false
		}
		ps.Spread, ps.SpreadBook = true, !ps.SpreadBook
		ps.changed()
		return true
	})
}
