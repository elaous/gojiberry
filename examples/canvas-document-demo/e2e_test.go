//go:build !(js && wasm)

package main

import (
	"fmt"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// pageJS is the helpers the test evaluates in the page.
const pageJS = `window.T = {
  s: () => JSON.parse(document.getElementById('state').textContent),
  near: (a, b, eps = 1.5) => Math.abs(a - b) <= eps,
  // Does any laid-out line intrude on rect [x, y, w, h]?
  intrudes: (lines, [x, y, w, h]) => lines.some((l) => l.y < y + h && l.y + l.h > y && l.x < x + w && l.x + l.w > x),
  grow: ([x, y, w, h], m = 8) => [x - m, y - m, w + 2 * m, h + 2 * m],
};
T.vp = T.s().viewport;
// Content (document) coordinates -> canvas coordinates, given the scroll.
T.view = (cx, cy) => { const s = T.s(); return [T.vp[0] + cx - s.scroll[0], T.vp[1] + cy - s.scroll[1]]; };
true`

// TestEndToEnd drives the demo in headless Chromium: the page layout (breaks,
// column configs persisting across pages, headers/footers/page numbers),
// scrolling and clipping, then in edit mode dragging a column grip, moving the
// picture (snapping, with text re-wrapping around it) and editing/formatting the
// floating note.
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Eval(pageJS, nil)
	ok := d.OK
	// is evaluates a JavaScript expression of the current state s and the helpers T.
	is := func(expr string) bool { return d.Bool(`(s => !!(` + expr + `))(T.s())`) }
	num := func(expr string) float64 { var v float64; d.Eval(`(s => `+expr+`)(T.s())`, &v); return v }
	str := func(expr string) string { return d.Str(`(s => ` + expr + `)(T.s())`) }
	js := func(expr string) any { var v any; d.Eval(`(s => `+expr+`)(T.s())`, &v); return v }
	view := func(cx, cy float64) (float64, float64) {
		var p []float64
		d.Eval(fmt.Sprintf("T.view(%v, %v)", cx, cy), &p)
		return p[0], p[1]
	}
	fl := func(expr string) []float64 { var v []float64; d.Eval(`(s => `+expr+`)(T.s())`, &v); return v }
	var vp []float64
	d.Eval("T.vp", &vp)
	vx, vy, vw, vh := vp[0], vp[1], vp[2], vp[3]
	scrollTo := func(y float64) {
		d.CanvasMove(vx+vw/2, vy+vh/2)
		d.Wheel(0, y-num("s.scroll[1]"))
	}
	clickView := func(cx, cy float64) { x, y := view(cx, cy); d.CanvasClick(x, y) }
	dblView := func(cx, cy float64) { x, y := view(cx, cy); d.CanvasDblClick(x, y) }
	dragView := func(ax, ay, bx, by float64) {
		x0, y0 := view(ax, ay)
		x1, y1 := view(bx, by)
		d.CanvasDrag(x0, y0, x1, y1)
	}
	page := func(n int) string { return fmt.Sprintf("s.lines.filter((l) => l.page === %d)", n) }

	// --- layout ---
	ok(is("s.pageCount === 4 && s.pagesUsed === 4 && !s.overflow"), "text fills four pages without overflowing")
	ok(is("(k => k.has('natural') && k.has('page') && k.has('column'))(new Set(s.breaks.map((b) => b.kind)))"), "natural, user page and user column breaks all occur")
	ok(is("(b => b.page === 1 && b.col === 0)(s.breaks.find((b) => b.kind === 'column'))"), "the column break was in the first column of page 1")
	ok(is("s.lines.some((l) => l.page === 1 && l.col === 1)"), "text after it continues in the next column")
	ok(is("s.bands.some((b) => b.page === 0 && b.xs.length === 2)"), `two columns on page 0 (set by \columns 2)`)
	ok(is("s.bands.some((b) => b.page === 1 && b.xs.length === 2)"), "the column config carries on to page 1 (from a natural break)")
	ok(is("s.breaks.some((b) => b.kind === 'natural' && b.page === 0 && b.col === 1)"), "a natural page break after the last column of page 0")

	// The Markdown transform: formats, an inline image fitted into the page, a table.
	for _, k := range []string{"underline", "strike", "super", "sub", "background"} {
		ok(is("s.decorations."+k+" > 0"), "markdown produced a "+k+" run")
	}
	ok(is(`(img => { const [px, py, pw] = s.pageRects[img.page]; return img.h <= 107 && img.x >= px + 50 && img.x + img.w <= px + pw - 50; })(s.objects.find((o) => o.kind === 'image'))`), "the inline image sits inside the page margins")
	ok(is("s.objects.some((o) => o.kind === 'table' && o.page === 2)"), "the markdown table is laid out as a block object")
	ok(is(`(pb => T.near(s.lines.find((l) => l.page === pb.page + 1).y, s.pageRects[pb.page + 1][1] + 60))(s.breaks.find((b) => b.kind === 'page'))`), "a user page break resumes at the top of the next page text area")
	ok(is("s.bands.some((b) => b.page === 3 && b.xs.length === 1)"), "later pages use the single-column config set after the columns")
	ok(is(`JSON.stringify(s.pageNumbers) === JSON.stringify(['', 'Page 1', 'Page 2', '— i —'])`), fmt.Sprint("per-page page-number configs: ", js("s.pageNumbers")))

	// --- scrolling and clipping ---
	ok(is("s.content[1] > s.viewport[3]"), "the document is taller than the viewport")
	ok(d.Pixel(int(vx+vw)+6, int(vy)+100)[3] == 0 && d.Pixel(int(vx)+100, int(vy+vh)+6)[3] == 0, "the pages are clipped to the viewport")
	scrollTo(450)
	ok(num("s.scroll[1]") == 450, "wheel scrolls the document")
	hx, hy := view(num("s.pageRects[1][0]")+60, num("s.pageRects[1][1]")+28)
	ok(d.Pixel(int(hx), int(hy))[3] != 0, "the scrolled pages are redrawn")

	// --- edit mode ---
	d.ToggleMode()

	// Drag a column grip on page 0: the config is shared, so page 1's columns follow.
	scrollTo(150)
	band0 := fl(`(b => [...b.widths, b.gap, b.xs[0], b.top])(s.bands.find((b) => b.page === 0 && b.xs.length === 2))`)
	w0, w1, gap, x0, top := band0[0], band0[1], band0[2], band0[3], band0[4]
	gx, gy := view(x0+w0+gap/2, top-2-8)
	d.CanvasDrag(gx, gy, gx+30, gy)
	after0 := fl(`s.bands.find((b) => b.page === 0 && b.xs.length === 2).widths`)
	after1 := fl(`s.bands.find((b) => b.page === 1 && b.xs.length === 2).widths`)
	near := func(a, b float64) bool { return a-b <= 1.5 && b-a <= 1.5 }
	ok(near(after0[0], w0+30) && near(after0[1], w1-30), fmt.Sprint("dragging the grip resizes the two columns: ", after0))
	ok(near(after1[0], after0[0]), "the next page keeps the new column widths")
	ok(is("!s.overflow && s.pagesUsed === 4"), "the document still fits")

	// Headers, footers and page numbers: double click to edit; per page =
	// previous page or new; the page number is selectable and has settings.
	ok(is("s.headers[1] === 'Gojiberry Quarterly' && s.headers[2] === 'Gojiberry Quarterly' && s.headers[3] === ''"), "headers inherit down the pages (page 3 removed it)")
	scrollTo(num("s.pageRects[1][1]") - 40)
	h1x, h1y := view(num("s.pageRects[1][0]")+100, num("s.pageRects[1][1]")+30)
	d.CanvasMove(h1x, h1y)
	d.CanvasDblClick(h1x, h1y)
	ok(is("s.hfEdit.editing && s.hfEdit.page === 1 && !s.hfEdit.footer"), "double click on a header starts editing it")
	ok(is("s.headers[1] === 'Gojiberry Quarterly'"), "looking at an inherited header does not change anything")
	d.Type(" (draft)")
	ok(is("s.headers[0] === 'Gojiberry Quarterly' && s.headers[1] === 'Gojiberry Quarterly (draft)' && s.headers[2] === 'Gojiberry Quarterly (draft)'"), fmt.Sprint("typing makes page 1 its own header, which page 2 inherits: ", js("s.headers")))
	d.Press("Escape")

	// Select page number 2 (the third page) and change its format.
	scrollTo(num("s.pageRects[2][1]") + num("s.pageRects[2][3]") - 400)
	nr := fl("s.numberRects[2]")
	nx, ny := view(nr[0]+nr[2]/2, nr[1]+nr[3]/2)
	d.CanvasClick(nx, ny)
	ok(is("s.numberSelected.ok && s.numberSelected.page === 2 && s.menu.open"), "clicking a page number selects it and opens its settings menu")
	labelIdx := int(num("s.menu.labels.findIndex((l) => l.trim().endsWith('Page {n} of {total}'))"))
	ok(labelIdx >= 0, fmt.Sprint("the menu offers formats: ", js("s.menu.labels.join(' | ')")))
	// Menu rows: 28px items, 9px separators (labelled "").
	menu := fl("s.menu.rect")
	var labels []string
	d.Eval("T.s().menu.labels", &labels)
	rowTop := menu[1]
	for i := 0; i < labelIdx; i++ {
		if labels[i] == "" {
			rowTop += 9
		} else {
			rowTop += 28
		}
	}
	pmx, pmy := view(menu[0]+20, rowTop+14)
	d.CanvasClick(pmx, pmy)
	ok(is("s.pageNumbers[2] === 'Page 2 of 4' && s.pageNumbers[1] === 'Page 1' && s.pageNumbers[3] === '— i —'"), fmt.Sprint("the format applies from that page on, earlier pages keep theirs: ", js("s.pageNumbers")))
	ok(is("!s.menu.open"), "the menu closes")

	// Image: move (snaps to 5) and the text wraps around it.
	scrollTo(num("s.image.visible[1]") - 200)
	ok(is("s.image.wrap && !T.intrudes("+page(1)+", T.grow(s.image.visible))"), "text wraps around the picture")
	im := fl("s.image.visible")
	ix, iy := view(im[0]+im[2]/2, im[1]+im[3]/2)
	d.CanvasDrag(ix, iy, ix-83, iy)
	ok(is("s.image.anchor.Page === 1 && s.image.anchor.X === 245 && s.image.anchor.Y === 150"), fmt.Sprint("the anchored picture moved left, snapping to 5 relative to its page: ", js("s.image.anchor")))
	ok(is("!T.intrudes("+page(1)+", T.grow(s.image.visible))"), "text re-wrapped around the moved picture")
	// Wrap button (4th in the toolbar) off: text ignores the picture.
	im = fl("s.image.visible")
	wx, wy := view(im[0]+3*56+10, im[1]-17)
	d.CanvasClick(wx, wy)
	ok(is("!s.image.wrap && T.intrudes("+page(1)+", s.image.visible)"), "with wrap off, text runs under the picture")
	d.CanvasClick(wx, wy)
	ok(is("s.image.wrap"), "wrap back on")

	// Floating note: edit and format.
	scrollTo(num("s.floating.y") - 220)
	f := fl("[s.floating.x, s.floating.y, s.floating.w]")
	fX, fY, fW := f[0], f[1], f[2]
	ok(is("s.floating.wrap && s.floating.text.startsWith('Floating note')"), "floating note is there")
	clickView(fX+fW-14, fY+14) // right of the first line of text: caret at its end
	ok(is("s.floating.editing"), "clicking into the note starts editing")
	d.Press("Shift+Home")
	sel := fl("s.floating.selection")
	from, to := sel[0], sel[1]
	ok(to-from > 5, fmt.Sprint("shift+Home selects the line: ", sel))
	clickView(fX+44+4+14, fY-17) // B
	ok(is(fmt.Sprintf("(bold => bold.length === 1 && bold[0].Text.length === %v)(s.floating.items.filter((it) => it.Format && it.Format.FontWeight === 700))", to-from)), fmt.Sprint("B bolds exactly the selection: ", js("JSON.stringify(s.floating.items)")))
	ok(is(fmt.Sprintf("s.floating.editing && s.floating.selection[1] - s.floating.selection[0] === %v", to-from)), "the selection survives the toolbar click")
	d.Press("End")
	d.Type("!")
	ok(is("s.floating.text.includes('!')"), fmt.Sprint("typing inserts text: ", str("s.floating.text")))
	d.Press("Escape")
	ok(is("!s.floating.editing"), "Escape stops editing")

	// --- more formatting: text color and highlight from the toolbar palettes ---
	x, y := view(fX+fW-14, fY+14)
	d.CanvasMove(x, y)
	d.CanvasClick(x, y)
	d.Press("Shift+Home")
	pick := func(button string, index float64) {
		b := fl("s.floating.buttons." + button)
		clickView(b[0]+b[2]/2, b[1]+b[3]/2)
		ok(is("s.floating.palette.open"), button+" button opens its palette: "+str("s.floating.palette.labels.join(',')"))
		p := fl("s.floating.palette.rect")
		clickView(p[0]+20, p[1]+28*index+14)
		ok(is("!s.floating.palette.open && s.floating.editing"), "choosing a swatch closes the palette and keeps editing")
	}
	pick("color", 2) // Red
	ok(is("s.floating.items.some((it) => it.Format && it.Format.Color === '#d93025')"), "the selected text is colored red")
	pick("mark", 0) // Yellow
	ok(is("s.floating.items.some((it) => it.Format && it.Format.Background === '#fff475' && it.Format.Color === '#d93025')"), "and highlighted, keeping the color")

	// --- auto-close pairs ---
	d.Press("ArrowRight") // collapse to the end of the selection
	d.Type(" (x")
	ok(is("s.floating.text.includes(' (x)') && s.floating.text.split(')').length === 2"), "typing ( inserts the closing ): "+str("s.floating.text"))
	d.Type(")")
	ok(is("!s.floating.text.includes('))') && s.floating.text.includes('(x)')"), "typing the closer overtypes it")
	d.Type("[")
	d.Press("Backspace")
	ok(is("!s.floating.text.includes('[') && !s.floating.text.includes(']')"), "Backspace between a pair deletes both")

	// --- auto-complete from the words in the text ---
	d.Type(" Flo")
	ok(is("s.floating.suggestions.includes('Floating')"), fmt.Sprint("typing a prefix offers completions: ", js("s.floating.suggestions")))
	d.Press("Tab")
	ok(is("s.floating.text.includes('(x) Floating edit') && s.floating.suggestions.length === 0"), "Tab accepts the suggestion: "+str("s.floating.text"))
	d.Type(" Qua")
	ok(is("s.floating.suggestions.includes('Quarterly')"), "the Vocabulary words are offered too")
	d.Press("Escape")
	ok(is("s.floating.suggestions.length === 0 && s.floating.editing"), "Escape dismisses the popup but keeps editing")

	// --- clipboard (through the browser's copy/paste events) ---
	d.Press("Control+A")
	ok(is("s.floating.selection[1] - s.floating.selection[0] === s.floating.text.length"), "Ctrl+A selects all the text")
	copied := d.Str(`(() => {
		const dt = new DataTransfer();
		document.getElementById('input').dispatchEvent(new ClipboardEvent('copy', { clipboardData: dt, bubbles: true, cancelable: true }));
		return dt.getData('text/plain');
	})()`)
	ok(copied == str("s.floating.text"), "copy puts the selection on the clipboard: "+copied)
	d.Eval(`(() => {
		const dt = new DataTransfer();
		dt.setData('text/plain', 'PASTED (');
		document.getElementById('input').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
	})()`, nil)
	ok(str("s.floating.text") == "PASTED (", "paste replaces the selection, without auto-closing: "+str("s.floating.text"))
	d.Press("Control+A")
	d.Eval(`document.getElementById('input').dispatchEvent(new ClipboardEvent('cut', { clipboardData: new DataTransfer(), bubbles: true, cancelable: true }))`, nil)
	ok(str("s.floating.text") == "", "cut removes the selection")
	d.Type("Floating note")

	// --- select text in the document, copy its format, paste it into the note ---
	scrollTo(0)
	hd := fl("(l => [l.x, l.y])(s.lines.find((l) => l.page === 0))")
	dblView(hd[0]+20, hd[1]+14)
	ok(str("s.flowSelection.text") == "Quarterly", "double click selects a word of the document: "+str("s.flowSelection.text"))
	d.Press("Control+Alt+C")
	ok(is("s.formatClipboard.has && s.formatClipboard.format.Scale === 2 && s.formatClipboard.format.FontWeight === 700"), fmt.Sprint("Ctrl+Alt+C copies the selection's common format: ", js("JSON.stringify(s.formatClipboard.format)")))
	// A drag-selection across differently formatted text copies only what they share.
	body := fl("(l => [l.x, l.y])(s.lines.filter((l) => l.page === 0)[2])")
	dragView(hd[0]+2, hd[1]+14, body[0]+150, body[1]+8)
	ok(is("s.flowSelection.text.includes('Quarterly Report') && s.flowSelection.text.includes('Markdown')"), "dragging selects across paragraphs")
	d.Press("Control+Alt+C")
	ok(is("s.formatClipboard.format.Scale === 0 && s.formatClipboard.format.FontWeight === 0"), "a mixed selection has no common size or weight")
	// Back to the heading word, then into the note.
	dblView(hd[0]+20, hd[1]+14)
	d.Press("Control+Alt+C")
	f = fl("[s.floating.x, s.floating.y, s.floating.w]")
	fX, fY, fW = f[0], f[1], f[2]
	scrollTo(fY - 220)
	clickView(fX+fW-14, fY+14)
	d.Press("Control+A")
	d.Press("Control+Alt+V")
	ok(is("!s.flowSelection.text && s.floating.items.length === 1 && s.floating.items[0].Format.Scale === 2 && s.floating.items[0].Format.FontWeight === 700"), fmt.Sprint("Ctrl+Alt+V paints the format onto the note: ", js("JSON.stringify(s.floating.items)")))
	d.Press("Control+B")
	ok(is("s.floating.items[0].Format.FontWeight === 400"), "Ctrl+B toggles bold off (keymap command)")

	// --- registered features: multi-cursor, line operations, Markdown clipboard ---
	d.Type("a a a")
	d.Press("Home")
	d.Press("ArrowRight")
	for range 3 {
		d.Press("Control+D")
	}
	ok(is("s.floating.cursors === 3 && s.floating.text === 'a a a'"), fmt.Sprint("Ctrl+D selects the word, then adds the next two matches: ", js("s.floating.cursors")))
	d.Type("Z")
	ok(str("s.floating.text") == "Z Z Z", "typing replaces every selected match: "+str("s.floating.text"))
	d.Press("Escape")
	ok(is("s.floating.cursors === 1 && s.floating.editing"), "Escape goes back to one cursor and keeps editing")
	d.Press("Control+A")
	d.Type("one")
	d.Press("Enter")
	d.Type("two")
	d.Press("Alt+ArrowUp")
	ok(str("s.floating.text") == "two\none", "Alt+Up moves the line up")
	d.Press("Alt+Shift+ArrowDown")
	ok(str("s.floating.text") == "two\ntwo\none", "Alt+Shift+Down copies the line down")
	d.Press("Control+Shift+K")
	ok(str("s.floating.text") == "two\none", "Ctrl+Shift+K deletes the line")
	d.Press("Control+A")
	d.Press("Alt+Shift+I")
	ok(num("s.floating.cursors") == 2, "Alt+Shift+I puts a cursor at the end of each selected line")
	d.Press("Escape")

	// Markdown clipboard (the page needs clipboard permission).
	d.GrantClipboard()
	d.Press("Control+A")
	d.Press("Control+Shift+C")
	d.Sleep(100)
	md := d.Str(`navigator.clipboard.readText()`)
	// The note's text is at heading size (Scale 2), which Markdown writes as '#'.
	ok(md == "# two\n# one", fmt.Sprintf("Ctrl+Shift+C copies the selection as Markdown: %q", md))
	d.Eval(`navigator.clipboard.writeText('plain **loud** ~~old~~')`, nil)
	d.Press("Control+A")
	d.Press("Control+Shift+V")
	d.Sleep(100)
	ok(str("s.floating.text") == "plain loud old", fmt.Sprintf("Ctrl+Shift+V pastes Markdown as text, not as marks: %q", str("s.floating.text")))
	ok(is("(loud => loud && loud.Format.FontWeight === 700)(s.floating.items.find((i) => i.Text === 'loud')) && (old => old && old.Format.Strike)(s.floating.items.find((i) => i.Text === 'old'))"), "the pasted text carries bold and strike formatting")
	d.Press("Escape")

	// --- the floating table: text wraps around it; its icon drags it ---
	scrollTo(num("s.table.rect[1]") - 260)
	ok(is("s.table.wrap && !T.intrudes("+page(3)+", T.grow(s.table.rect))"), "text wraps around the floating table")
	ok(is("s.table.anchor.Page === 3 && s.table.anchor.X === 300"), "the table is anchored to page 3")
	tr := fl("s.table.rect")
	ix, iy = view(tr[0]-12, tr[1]-12)
	d.CanvasDrag(ix, iy, ix-53, iy)
	ok(is("s.table.anchor.X === 245 && s.table.anchor.Page === 3"), fmt.Sprint("dragging the table icon moves the table, snapping to 5 relative to its page: ", js("s.table.anchor")))
	ok(is("!T.intrudes("+page(3)+", T.grow(s.table.rect)) && !s.overflow"), "text re-wrapped around the moved table")

	// Move the note by its toolbar's Move button: both axes, snapping.
	scrollTo(num("s.floating.y") - 220)
	f = fl("[s.floating.x, s.floating.y]")
	mx, my := view(f[0]+20, f[1]-17)
	d.CanvasDrag(mx, my, mx+22, my+17)
	ok(is(fmt.Sprintf("s.floating.x === %v && s.floating.y === %v", f[0]+20, f[1]+15)), fmt.Sprintf("the note moved, snapped to 5: %v,%v -> %v,%v", f[0], f[1], num("s.floating.x"), num("s.floating.y")))
	d.NoErrors()
}
