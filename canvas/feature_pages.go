package canvas

// PageEditor is implemented by targets that flow into sheets.
type PageEditor interface {
	// PageSet returns the sheets, or nil.
	PageSet() *PageSet
	// CaretPage is the sheet the primary cursor is on.
	CaretPage() int
}

var _ PageEditor = (*TextFlow)(nil)

func (f *TextFlow) PageSet() *PageSet { return f.Pages }

func (f *TextFlow) CaretPage() int {
	if ln, ok := f.lineAt(f.sel.caret); ok {
		return ln.Page
	}
	return 0
}

// PageFurniture commands remove headers, footers and other page furniture.
type PageFurniture struct{}

func (PageFurniture) Name() string { return "page-furniture" }

// Command names are page.<what>.remove-<scope>.
const (
	CmdSetWatermark Command = "page.watermark.set"
	CmdSpread       Command = "page.spread"
	CmdSpreadBook   Command = "page.spread-book"
)

// CmdPageRemove names the command removing what from a scope.
func CmdPageRemove(what string, scope RemoveScope) Command {
	return Command("page." + what + ".remove-" + [...]string{"page", "forward", "all"}[scope])
}

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
