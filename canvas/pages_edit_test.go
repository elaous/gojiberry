package canvas

import (
	"strings"
	"testing"
)

// Three 200x120 pages with margins L20 T40 R20 B40, Pad 24, Gap 24:
// page k is at x=24, y=24+144k. Header zone: y 24..64 (+144k); footer
// zone y 104..144 (+144k). Text is 12px/16px, 8px per rune.

func docPages() *PageSet {
	pg := func() Page {
		return Page{Width: 200, Height: 120, Margin: Insets{Left: 20, Top: 40, Right: 20, Bottom: 40}}
	}
	ps := NewPageSet(pg(), pg(), pg())
	ps.SetPainter(&fakePainter{})
	ps.SetMode(ModeEdit)
	return ps
}

// headerPoint is a point inside page k's header zone.
func headerPoint(k int) (float64, float64) { return 100, 40 + 144*float64(k) }
func footerPoint(k int) (float64, float64) { return 100, 124 + 144*float64(k) }

func typeInto(ps *PageSet, s string) {
	for _, r := range s {
		ps.InsertText(string(r))
	}
}

func TestDoubleClickEditsTheHeaderAndLaterPagesInherit(t *testing.T) {
	ps := docPages()
	changes := 0
	ps.OnChange = func(*PageSet) { changes++ }
	if !ps.DoubleClick(headerPoint(0)) {
		t.Fatal("double click in the header area starts editing")
	}
	if page, footer, ok := ps.Editing(); !ok || page != 0 || footer {
		t.Fatalf("editing = %d %v %v", page, footer, ok)
	}
	if !ps.WantsFocus() {
		t.Fatal("editing needs keyboard focus")
	}
	typeInto(ps, "Hi")
	if h := ps.Pages[0].Header; h == nil || h.Text != "Hi" {
		t.Fatalf("header = %+v", ps.Pages[0].Header)
	}
	if ps.ResolvedHeader(2) == nil || ps.ResolvedHeader(2).Text != "Hi" {
		t.Fatal("later pages inherit the new header")
	}
	if changes != 2 {
		t.Fatalf("OnChange fired %d times, want once per change", changes)
	}
}

func TestEditingAnInheritedHeaderCreatesANewOneOnlyOnChange(t *testing.T) {
	ps := docPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "Report", Align: AlignRight}
	ps.DoubleClick(headerPoint(1))
	if ps.Pages[1].Header != nil {
		t.Fatal("just looking must not break the inheritance")
	}
	ps.HandleKey("Escape", false)
	ps.DoubleClick(headerPoint(1))
	ps.InsertText("!")
	own := ps.Pages[1].Header
	if own == nil || own.Text != "Report!" || own.Align != AlignRight {
		t.Fatalf("page 1 header = %+v (it starts from the inherited text and look)", own)
	}
	if ps.Pages[0].Header.Text != "Report" {
		t.Fatal("page 0 is untouched")
	}
	if ps.ResolvedHeader(2).Text != "Report!" {
		t.Fatal("page 2 now inherits page 1's header")
	}
}

func TestEmptyingAHeaderRemovesItFromThatPageOn(t *testing.T) {
	ps := docPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "ab"}
	ps.DoubleClick(headerPoint(1))
	ps.HandleKey("Backspace", false)
	ps.HandleKey("Backspace", false)
	if ps.ResolvedHeader(0) == nil || ps.ResolvedHeader(1) != nil || ps.ResolvedHeader(2) != nil {
		t.Fatal("page 0 keeps its header; pages 1 and 2 have none")
	}
	if h := ps.Pages[1].Header; h == nil || h.Text != "" {
		t.Fatalf("an empty header is explicit: %+v", h)
	}
}

func TestHeaderEditKeys(t *testing.T) {
	ps := docPages()
	ps.DoubleClick(headerPoint(0))
	typeInto(ps, "abc")
	ps.HandleKey("Enter", false)
	typeInto(ps, "de")
	if ps.Pages[0].Header.Text != "abc\nde" {
		t.Fatalf("text = %q", ps.Pages[0].Header.Text)
	}
	ps.HandleKey("ArrowUp", false) // caret was at 6 (col 2 of line 2) -> col 2 of line 1
	ps.InsertText("X")
	if ps.Pages[0].Header.Text != "abXc\nde" {
		t.Fatalf("text = %q", ps.Pages[0].Header.Text)
	}
	ps.HandleKey("ArrowDown", false)
	ps.HandleKey("End", false)
	ps.InsertText("Y")
	ps.HandleKey("Home", false)
	ps.InsertText("Z")
	if ps.Pages[0].Header.Text != "abXc\nZdeY" {
		t.Fatalf("text = %q", ps.Pages[0].Header.Text)
	}
	ps.HandleKey("ArrowLeft", false)
	ps.HandleKey("Delete", false)
	ps.HandleKey("ArrowRight", false)
	ps.HandleKey("ArrowRight", false)
	ps.HandleKey("ArrowRight", false) // clamps at the end
	ps.HandleKey("ArrowDown", false)  // already on the last line
	if ps.Pages[0].Header.Text != "abXc\ndeY" {
		t.Fatalf("text = %q", ps.Pages[0].Header.Text)
	}
	ps.HandleKey("Home", false)
	ps.HandleKey("ArrowLeft", false)
	ps.HandleKey("ArrowLeft", false)
	ps.HandleKey("ArrowUp", false) // first line: nothing above
	if !ps.HandleKey("Escape", false) || ps.edit != nil {
		t.Fatal("Escape ends the edit")
	}
	if ps.HandleKey("x", false) || ps.InsertText("x") {
		t.Fatal("not editing any more")
	}
}

func TestTallHeaderShrinksTheTextAreaWhileTyping(t *testing.T) {
	ps := docPages()
	_, _, _, h0 := ps.ContentRect(0)
	ps.DoubleClick(headerPoint(0))
	for i := 0; i < 4; i++ {
		ps.HandleKey("Enter", false)
	}
	_, _, _, h1 := ps.ContentRect(0)
	if h1 >= h0 {
		t.Fatalf("a 5-line header (80px) must push the text area down: %g -> %g", h0, h1)
	}
}

func TestClickPlacesTheCaretAndClickingElsewhereEndsTheEdit(t *testing.T) {
	ps := docPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "abcdef"}
	ps.DoubleClick(headerPoint(0))
	// Header text is left-aligned at x=44 (page x 24 + margin 20), 8px per rune.
	if !ps.MouseDown(44+8*2+1, 30) || ps.edit.caret != 2 {
		t.Fatalf("caret = %d", ps.edit.caret)
	}
	ps.MouseDown(200, 30)
	if ps.edit.caret != 6 {
		t.Fatalf("clicking right of the text puts the caret at its end: %d", ps.edit.caret)
	}
	if !ps.MouseDown(100, 70) || ps.edit != nil { // in the text area
		t.Fatal("clicking outside the edited area ends the edit")
	}
}

func TestBadgeShowsInheritanceAndUsePrevious(t *testing.T) {
	ps := docPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "Report"}
	ps.DoubleClick(headerPoint(1))
	label, _, buttons := ps.badge()
	if label != "Header · same as previous" || len(buttons) != 4 || buttons[0].label != "Remove ▾" || buttons[1].label != "Left" {
		t.Fatalf("label=%q buttons=%v", label, buttons)
	}
	ps.InsertText("!")
	label, _, buttons = ps.badge()
	if label != "Header · this page" || buttons[0].label != "Use previous" {
		t.Fatalf("label=%q first=%q", label, buttons[0].label)
	}
	r := buttons[0].rect
	if !ps.MouseDown(r[0]+3, r[1]+3) {
		t.Fatal("badge button press")
	}
	if ps.Pages[1].Header != nil || ps.edit != nil {
		t.Fatal("'Use previous' restores the inheritance and ends the edit")
	}
	ps.DoubleClick(headerPoint(0))
	if label, _, _ := ps.badge(); label != "Header · this page" {
		t.Fatalf("page 0 has its own header: %q", label)
	}
	if _, _, buttons := ps.badge(); buttons[0].label == "Use previous" {
		t.Fatal("the first page has nothing to go back to")
	}
	ps.Pages[0].Header = nil
	if label, _, _ := ps.badge(); label != "Header · all pages" {
		t.Fatalf("page 0 with no header of its own: %q", label)
	}
}

func TestBadgeAlignButtons(t *testing.T) {
	ps := docPages()
	ps.DoubleClick(footerPoint(1))
	typeInto(ps, "Foot")
	_, _, buttons := ps.badge()
	var center badgeButton
	for _, b := range buttons {
		if b.label == "Center" {
			center = b
		}
	}
	ps.MouseDown(center.rect[0]+3, center.rect[1]+3)
	if ps.Pages[1].Footer.Align != AlignCenter || ps.edit.base.Align != AlignCenter {
		t.Fatalf("footer = %+v", ps.Pages[1].Footer)
	}
	if ps.ResolvedFooter(2).Align != AlignCenter {
		t.Fatal("page 2 inherits the alignment too")
	}
	// Left alignment of a not-yet-owned page creates its own footer.
	ps.HandleKey("Escape", false)
	ps.DoubleClick(footerPoint(2))
	_, _, buttons = ps.badge()
	for _, b := range buttons {
		if b.label == "Right" {
			ps.MouseDown(b.rect[0]+3, b.rect[1]+3)
		}
	}
	if f := ps.Pages[2].Footer; f == nil || f.Align != AlignRight || f.Text != "Foot" {
		t.Fatalf("footer = %+v", ps.Pages[2].Footer)
	}
}

func TestFooterBadgeAddsAndHidesThePageNumber(t *testing.T) {
	ps := docPages()
	ps.DoubleClick(footerPoint(1))
	_, _, buttons := ps.badge()
	last := buttons[len(buttons)-1]
	if last.label != "# Page number" {
		t.Fatalf("last button = %q", last.label)
	}
	ps.MouseDown(last.rect[0]+3, last.rect[1]+3)
	if cfg, n := ps.ResolvedPageNumber(1); cfg == nil || n != 2 || ps.PageNumberText(2) != "3" {
		t.Fatalf("number on page 1: cfg=%v n=%d; page 2 text %q", cfg, n, ps.PageNumberText(2))
	}
	if ps.PageNumberText(0) != "" {
		t.Fatal("earlier pages are unaffected")
	}
	_, _, buttons = ps.badge()
	if buttons[len(buttons)-1].label != "Hide page number" {
		t.Fatalf("last button = %q", buttons[len(buttons)-1].label)
	}
	b := buttons[len(buttons)-1]
	ps.MouseDown(b.rect[0]+3, b.rect[1]+3)
	if ps.PageNumberText(1) != "" || ps.PageNumberText(2) != "" {
		t.Fatal("hidden from page 1 on")
	}
}

// --- page number selection and its menu ---

func numbered() *PageSet {
	ps := docPages()
	ps.Pages[0].PageNumber = &PageNumberConfig{Show: true, Start: 1, Format: "Page {n}", Align: AlignRight}
	return ps
}

// numberPoint is a point on page k's number text.
func numberPoint(t *testing.T, ps *PageSet, k int) (float64, float64) {
	t.Helper()
	r, ok := ps.numberRect(k)
	if !ok {
		t.Fatalf("page %d has no number", k)
	}
	return r[0] + r[2]/2, r[1] + r[3]/2
}

func pick(t *testing.T, ps *PageSet, label string) {
	t.Helper()
	m := ps.Menu()
	if !m.IsOpen() {
		t.Fatal("menu not open")
	}
	top := m.y
	for _, it := range m.items {
		if !it.Separator && strings.TrimSpace(strings.TrimPrefix(it.Label, "✓")) == label {
			if it.Disabled {
				t.Fatalf("%q is disabled", label)
			}
			ps.MouseDown(m.x+5, top+m.itemHeight(it)/2)
			return
		}
		top += m.itemHeight(it)
	}
	t.Fatalf("no menu item %q in %+v", label, m.items)
}

func TestClickingThePageNumberSelectsItAndOpensTheMenu(t *testing.T) {
	ps := numbered()
	x, y := numberPoint(t, ps, 1)
	if !ps.MouseDown(x, y) {
		t.Fatal("press on the number")
	}
	if page, ok := ps.SelectedPageNumber(); !ok || page != 1 || !ps.Menu().IsOpen() || !ps.WantsFocus() {
		t.Fatalf("selected=%d,%v open=%v", page, ok, ps.Menu().IsOpen())
	}
	var labels []string
	for _, it := range ps.Menu().items {
		labels = append(labels, it.Label)
	}
	for _, want := range []string{"Hide page number", "   Align left", "✓ Align right", "✓ 1, 2, 3", "✓ Page {n}", "Same as previous page"} {
		found := false
		for _, l := range labels {
			found = found || l == want
		}
		if !found {
			t.Errorf("menu lacks %q: %q", want, labels)
		}
	}
	// "Same as previous page" is disabled: page 1 has no config of its own.
	for _, it := range ps.Menu().items {
		if it.Label == "Same as previous page" && !it.Disabled {
			t.Fatal("nothing to revert to on an inheriting page")
		}
	}
}

func TestPageNumberMenuActionsCreateANewConfigOnThatPage(t *testing.T) {
	ps := numbered()
	x, y := numberPoint(t, ps, 1)
	ps.MouseDown(x, y)
	pick(t, ps, "i, ii, iii")
	cfg := ps.Pages[1].PageNumber
	if cfg == nil || cfg.Style != NumLowerRoman || cfg.Start != 2 || cfg.Format != "Page {n}" || cfg.Align != AlignRight {
		t.Fatalf("page 1 config = %+v: a copy of the inherited one, numbered so page 1 stays 2", cfg)
	}
	if ps.PageNumberText(0) != "Page 1" || ps.PageNumberText(1) != "Page ii" || ps.PageNumberText(2) != "Page iii" {
		t.Fatalf("numbers: %q %q %q", ps.PageNumberText(0), ps.PageNumberText(1), ps.PageNumberText(2))
	}
	if ps.Pages[0].PageNumber.Style != NumArabic {
		t.Fatal("page 0's own config is untouched")
	}
	if ps.Menu().IsOpen() {
		t.Fatal("choosing closes the menu")
	}
}

func TestPageNumberMenuFormatsAlignmentRestartAndRevert(t *testing.T) {
	ps := numbered()
	open := func(k int) {
		x, y := numberPoint(t, ps, k)
		ps.MouseDown(x, y)
	}
	open(2)
	pick(t, ps, "Page {n} of {total}")
	if ps.PageNumberText(2) != "Page 3 of 3" {
		t.Fatalf("text = %q", ps.PageNumberText(2))
	}
	open(2)
	pick(t, ps, "Align left")
	if ps.Pages[2].PageNumber.Align != AlignLeft || ps.Pages[0].PageNumber.Align != AlignRight {
		t.Fatal("alignment applies to the new config only")
	}
	open(2)
	pick(t, ps, "Restart numbering at 1 here")
	if ps.PageNumberText(2) != "Page 1 of 3" {
		t.Fatalf("text = %q", ps.PageNumberText(2))
	}
	open(2)
	pick(t, ps, "Same as previous page")
	if ps.Pages[2].PageNumber != nil || ps.PageNumberText(2) != "Page 3" {
		t.Fatalf("reverted: %v %q", ps.Pages[2].PageNumber, ps.PageNumberText(2))
	}
	open(1)
	pick(t, ps, "Hide page number")
	if ps.PageNumberText(1) != "" || ps.PageNumberText(2) != "" || ps.PageNumberText(0) != "Page 1" {
		t.Fatalf("hidden from page 1: %q %q", ps.PageNumberText(1), ps.PageNumberText(2))
	}
}

func TestTurningNumbersBackOnReusesTheirLook(t *testing.T) {
	ps := numbered()
	ps.Pages[1].PageNumber = &PageNumberConfig{Show: false}
	ps.DoubleClick(footerPoint(2))
	_, _, buttons := ps.badge()
	add := buttons[len(buttons)-1]
	if add.label != "# Page number" {
		t.Fatalf("label = %q", add.label)
	}
	ps.MouseDown(add.rect[0]+3, add.rect[1]+3)
	cfg := ps.Pages[2].PageNumber
	if cfg == nil || !cfg.Show || cfg.Start != 3 {
		t.Fatalf("config = %+v", cfg)
	}
	// The latest explicit config at or before page 2 is the "off" one on page 1,
	// whose (empty) look is what is reused.
	if ps.PageNumberText(2) != "3" {
		t.Fatalf("text = %q", ps.PageNumberText(2))
	}
}

func TestDoubleClickingThePageNumberSelectsItToo(t *testing.T) {
	ps := numbered()
	x, y := numberPoint(t, ps, 1)
	if !ps.DoubleClick(x, y) || !ps.Menu().IsOpen() || ps.edit != nil {
		t.Fatal("double click on the number selects it, it doesn't edit the footer")
	}
	ps.HandleKey("Escape", false)
	if ps.Menu().IsOpen() {
		t.Fatal("Escape closes the menu")
	}
	if !ps.HandleKey("Escape", false) || func() bool { _, ok := ps.SelectedPageNumber(); return ok }() {
		t.Fatal("the next Escape clears the selection")
	}
	if ps.HandleKey("Escape", false) {
		t.Fatal("nothing left")
	}
}

func TestClickingElsewhereClearsTheNumberSelection(t *testing.T) {
	ps := numbered()
	x, y := numberPoint(t, ps, 0)
	ps.MouseDown(x, y)
	ps.Menu().Close()
	if !ps.MouseDown(100, 70) {
		t.Fatal("deselecting asks for a redraw")
	}
	if _, ok := ps.SelectedPageNumber(); ok {
		t.Fatal("selection cleared")
	}
	if ps.MouseDown(100, 70) {
		t.Fatal("nothing changed")
	}
}

func TestNumberSelectionWhileEditingAnotherAreaSwitches(t *testing.T) {
	ps := numbered()
	ps.DoubleClick(headerPoint(1))
	x, y := numberPoint(t, ps, 1)
	ps.MouseDown(x, y)
	if ps.edit != nil || !ps.Menu().IsOpen() {
		t.Fatal("clicking a page number ends a header edit and selects the number")
	}
}

func TestViewModeIgnoresHeaderFooterEditing(t *testing.T) {
	ps := numbered()
	ps.SetMode(ModeView)
	x, y := numberPoint(t, ps, 1)
	if ps.DoubleClick(headerPoint(0)) || ps.MouseDown(x, y) || ps.MouseMove(x, y) || ps.InsertText("x") || ps.HandleKey("a", false) || ps.CSSCursor() != "" || ps.WantsFocus() {
		t.Fatal("view mode is read-only")
	}
	ps.SetMode(ModeEdit)
	ps.DoubleClick(headerPoint(0))
	ps.SetMode(ModeView)
	if ps.edit != nil {
		t.Fatal("leaving edit mode ends the edit")
	}
	p := ps.Painter.(*fakePainter)
	p.calls = nil
	ps.DrawOverlay()
	if len(p.calls) != 0 {
		t.Fatal("no chrome in view mode")
	}
}

func TestPageSetCursorAndBlink(t *testing.T) {
	ps := numbered()
	x, y := numberPoint(t, ps, 0)
	ps.MouseMove(x, y)
	if ps.CSSCursor() != "pointer" {
		t.Fatalf("cursor over the number = %q", ps.CSSCursor())
	}
	ps.MouseMove(100, 40)
	if ps.CSSCursor() != "text" {
		t.Fatalf("cursor over the header area = %q", ps.CSSCursor())
	}
	ps.MouseMove(100, 80)
	if ps.CSSCursor() != "" {
		t.Fatalf("cursor over the text area = %q", ps.CSSCursor())
	}
	if ps.MouseMove(100, 81) || !ps.MouseMove(100, 40) || !ps.MouseLeave() || ps.MouseLeave() {
		t.Fatal("hover change reporting")
	}
	if ps.SetCaretVisible(false) {
		t.Fatal("blink only matters while editing")
	}
	ps.DoubleClick(headerPoint(0))
	if ps.SetCaretVisible(false) != true || ps.SetCaretVisible(false) {
		t.Fatal("blink reports redraw on change while editing")
	}
}

func TestEditOverlayDrawsOutlineCaretBadgeAndMenu(t *testing.T) {
	ps := numbered()
	ps.Pages[0].Header = &HeaderFooter{Text: "Report"}
	ps.DoubleClick(headerPoint(1))
	p := ps.Painter.(*fakePainter)
	p.calls = nil
	ps.DrawOverlay()
	for _, want := range []string{
		"strokeStyle #7c5cff",
		`fillText "Header · same as previous"`, `fillText "Left"`, `fillText "Center"`, `fillText "Right"`,
	} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	ps.HandleKey("Escape", false)
	x, y := numberPoint(t, ps, 0)
	ps.MouseDown(x, y)
	p.calls = nil
	ps.DrawOverlay()
	if !p.has("lineWidth 2") || !p.has(`fillText "Hide page number"`) {
		t.Fatalf("selection outline and menu: %v", p.calls)
	}
	ps.Draw()
	if !p.has(`fillText "Page 1"`) {
		t.Fatal("page number drawn")
	}
}

func TestEditedTextIsDrawnLive(t *testing.T) {
	ps := docPages()
	ps.DoubleClick(headerPoint(0))
	typeInto(ps, "Live")
	p := ps.Painter.(*fakePainter)
	p.calls = nil
	ps.Draw()
	if !p.has(`fillText "Live" 44 `) {
		t.Fatalf("calls: %v", p.calls)
	}
}

// --- stage plumbing ---

func TestStageDrawsOverlaysAfterEveryComponentAndRoutesDoubleClicks(t *testing.T) {
	var log []string
	a := &overlaySpy{spy: spy{name: "a", log: &log}}
	b := &spy{name: "b", log: &log}
	s := NewStage(&fakePainter{}, 1, 1, a, b)
	log = nil
	s.Paint()
	want := []string{"a:draw", "b:draw", "a:overlay"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", log, want)
	}
	if !s.DoubleClick(1, 2) || a.doubles != 1 {
		t.Fatal("double click not delivered")
	}
}

type overlaySpy struct {
	spy
	doubles int
}

func (o *overlaySpy) DrawOverlay()                  { *o.log = append(*o.log, "a:overlay") }
func (o *overlaySpy) DoubleClick(x, y float64) bool { o.doubles++; return true }

func TestScrollViewMapsDoubleClicksIntoContent(t *testing.T) {
	sv := NewScrollView(10, 20, 100, 80)
	sv.SetPainter(&fakePainter{})
	var log []string
	o := &overlaySpy{spy: spy{name: "a", log: &log}}
	sv.Add(o, 300, 400)
	sv.ScrollTo(30, 40)
	if !sv.DoubleClick(20, 50) || o.doubles != 1 {
		t.Fatal("inside the viewport")
	}
	if sv.DoubleClick(500, 500) || o.doubles != 1 {
		t.Fatal("outside the viewport")
	}
	track, _ := sv.bar(true)
	if sv.DoubleClick(track[0]+2, track[1]+2) || o.doubles != 1 {
		t.Fatal("a double click on a scrollbar isn't the content's")
	}
}

func TestPageNumberTotalPlaceholderAndFormats(t *testing.T) {
	ps := NewPageSet(
		Page{Width: 100, Height: 100, PageNumber: &PageNumberConfig{Show: true, Start: 1, Format: "{n}/{total}"}},
		Page{Width: 100, Height: 100},
	)
	if ps.PageNumberText(1) != "2/2" {
		t.Fatalf("text = %q", ps.PageNumberText(1))
	}
}
