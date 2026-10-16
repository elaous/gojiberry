package canvas

import (
	"strings"
	"testing"
)

func sheets(n int) *PageSet {
	var pages []Page
	for range n {
		pages = append(pages, Page{Width: 100, Height: 150, Margin: Insets{Left: 10, Top: 10, Right: 10, Bottom: 10}})
	}
	ps := NewPageSet(pages...)
	ps.Style.Pad, ps.Gap = 10, 20
	return ps
}

func rect(ps *PageSet, i int) [4]float64 {
	x, y, w, h := ps.PageRect(i)
	return [4]float64{x, y, w, h}
}

func TestSpreadLayout(t *testing.T) {
	ps := sheets(3)
	ps.Spread, ps.Gutter = true, 4
	if r := rect(ps, 0); r != [4]float64{10, 10, 100, 150} {
		t.Errorf("page 0 = %v", r)
	}
	if r := rect(ps, 1); r != [4]float64{114, 10, 100, 150} {
		t.Errorf("page 1 faces it: %v", r)
	}
	if r := rect(ps, 2); r != [4]float64{10, 180, 100, 150} {
		t.Errorf("page 2 starts the next row: %v", r)
	}
	if w, h := ps.Size(); w != 224 || h != 340 {
		t.Errorf("size = %g x %g", w, h)
	}
	if ps.PageAt(100) != 0 || ps.PageAt(170) != -1 || ps.PageAt(200) != 2 {
		t.Errorf("PageAt: %d %d %d", ps.PageAt(100), ps.PageAt(170), ps.PageAt(200))
	}
	if a := ps.AnchorAt(150, 60); a.Page != 1 || a.X != 36 || a.Y != 50 {
		t.Errorf("a point on the right sheet anchors to it: %+v", a)
	}
	if a := ps.AnchorAt(50, 60); a.Page != 0 {
		t.Errorf("left: %+v", a)
	}
	// An anchor beyond the sheet's edge reaches across the gutter.
	if x, _ := ps.Resolve(Anchor{Page: 0, X: 98}); x != 108 {
		t.Errorf("resolve = %g", x)
	}

	// A book: the first sheet stands alone on the right.
	ps.SpreadBook = true
	if r := rect(ps, 0); r != [4]float64{114, 10, 100, 150} {
		t.Errorf("book page 0 on the right: %v", r)
	}
	if r := rect(ps, 1); r != [4]float64{10, 180, 100, 150} {
		t.Errorf("book page 1 starts a row: %v", r)
	}
	if r := rect(ps, 2); r != [4]float64{114, 180, 100, 150} {
		t.Errorf("book page 2 faces it: %v", r)
	}
	// Centered in a wide container, and a lone last sheet.
	ps = sheets(3)
	ps.Spread, ps.Width = true, 500
	if r := rect(ps, 0); r[0] != 150 || rect(ps, 1)[0] != 250 {
		t.Errorf("centered pair: %v %v", rect(ps, 0), rect(ps, 1))
	}
	if r := rect(ps, 2); r[0] != 200 {
		t.Errorf("a lone sheet is centered: %v", r)
	}
	// Different heights share a row's top; the row is as tall as the tallest.
	ps = sheets(4)
	ps.Pages[1].Height = 200
	ps.Spread = true
	if _, h := ps.Size(); h != 10+200+20+150+10 {
		t.Errorf("row height: %g", h)
	}
	if rect(ps, 2)[1] != 10+200+20 {
		t.Errorf("next row below the tall sheet: %v", rect(ps, 2))
	}
}

func TestSpreadFlowsTextAcrossFacingPages(t *testing.T) {
	ps := sheets(2)
	ps.Spread = true
	f := NewTextFlow("", 0, 0, 0, 0)
	f.Pages = ps
	f.Items = []Item{Chars{Text: strings.Repeat("word ", 200)}}
	f.SetPainter(&fakePainter{})
	lay := f.Layout()
	left, right := 0, 0
	for _, ln := range lay.Lines {
		switch ln.Page {
		case 0:
			left++
			if ln.X >= 110 {
				t.Fatalf("page 0 text must be on the left sheet: %+v", ln)
			}
		case 1:
			right++
			if ln.X < 114 {
				t.Fatalf("page 1 text must be on the right sheet: %+v", ln)
			}
		}
	}
	if left == 0 || right == 0 {
		t.Errorf("lines on left %d, right %d", left, right)
	}
}

func TestWatermarkAndBackgroundInheritance(t *testing.T) {
	ps := sheets(4)
	if ps.ResolvedWatermark(2) != nil || ps.ResolvedBackground(2) != nil {
		t.Error("none by default")
	}
	ps.Pages[1].Watermark = &Watermark{Text: "DRAFT"}
	ps.Pages[3].Watermark = &Watermark{} // none from here on
	ps.Pages[1].Background = &Background{Image: fakeImage{50, 50}}
	got := []string{}
	for i := range 4 {
		w := ps.ResolvedWatermark(i)
		if w == nil {
			got = append(got, "-")
		} else {
			got = append(got, w.Text)
		}
	}
	if strings.Join(got, ",") != "-,DRAFT,DRAFT,-" {
		t.Errorf("watermarks = %v", got)
	}
	if ps.ResolvedBackground(0) != nil || ps.ResolvedBackground(3) == nil {
		t.Error("background inherits forward")
	}
	if !(*Watermark)(nil).None() || !(&Watermark{}).None() || (&Watermark{Text: "x"}).None() || (&Watermark{Image: fakeImage{1, 1}}).None() {
		t.Error("Watermark.None")
	}
	if !(*Background)(nil).None() || !(&Background{}).None() {
		t.Error("Background.None")
	}
}

func TestWatermarkAndBackgroundDrawing(t *testing.T) {
	ps := sheets(1)
	p := &fakePainter{}
	ps.SetPainter(p)
	ps.Pages[0].Watermark = &Watermark{Text: "DRAFT", Opacity: 0.3}
	ps.Draw()
	for _, want := range []string{"rotate -0.785", "alpha 0.3", `fillText "DRAFT" 0 0`, "translate 60 85", "font normal 700 72px sans-serif"} {
		if !p.has(want) {
			t.Errorf("text watermark lacks %q in %v", want, p.calls)
		}
	}
	p.calls = nil
	ps.Pages[0].Watermark = &Watermark{Text: "FLAT", Straight: true, Angle: 0}
	ps.Draw()
	if !p.has("rotate 0.000") || !p.has("alpha 0.15") {
		t.Errorf("straight, default opacity: %v", p.calls)
	}
	p.calls = nil
	ps.Pages[0].Watermark = &Watermark{Image: fakeImage{40, 20}, Scale: 0.5, Angle: 90}
	ps.Draw()
	if !p.has("rotate 1.571") || !p.has("drawImage 0 0 40 20 -> -25 -12.5 50 25") {
		t.Errorf("picture watermark: %v", p.calls)
	}

	for fit, want := range map[BackgroundFit]string{
		FitCover:   "drawImage 0 0 40 20 -> -90 10 300 150",
		FitContain: "drawImage 0 0 40 20 -> 10 60 100 50",
		FitStretch: "drawImage 0 0 40 20 -> 10 10 100 150",
	} {
		p.calls = nil
		ps.Pages[0].Watermark = nil
		ps.Pages[0].Background = &Background{Image: fakeImage{40, 20}, Opacity: 0.5, Fit: fit}
		ps.Draw()
		if !p.has(want) || !p.has("alpha 0.5") || !p.has("clipRect 10 10 100 150") {
			t.Errorf("fit %d: want %q in %v", fit, want, p.calls)
		}
	}
	// Without a painter there is nothing to draw on, and no crash.
	ps.Painter = nil
	ps.drawBackdrop(0)
}

func TestRemovingFurniture(t *testing.T) {
	mk := func() *PageSet {
		ps := sheets(5)
		ps.Pages[0].Header = &HeaderFooter{Text: "H"}
		ps.Pages[0].Footer = &HeaderFooter{Text: "F"}
		ps.Pages[0].PageNumber = &PageNumberConfig{Show: true, Start: 1, Format: "{n}"}
		ps.Pages[0].Watermark = &Watermark{Text: "W"}
		ps.Pages[0].Background = &Background{Image: fakeImage{1, 1}}
		return ps
	}
	// One row per page: header and footer, the number ("_" none), watermark and background.
	shown := func(ps *PageSet) string {
		flag := func(on bool, c string) string {
			if on {
				return c
			}
			return "-"
		}
		var rows []string
		for i := range ps.Pages {
			num := ps.PageNumberText(i)
			if num == "" {
				num = "_"
			}
			rows = append(rows, flag(ps.ResolvedHeader(i) != nil, "h")+flag(ps.ResolvedFooter(i) != nil, "f")+"|"+num+"|"+
				flag(ps.ResolvedWatermark(i) != nil, "w")+flag(ps.ResolvedBackground(i) != nil, "b"))
		}
		return strings.Join(rows, " ")
	}
	ps := mk()
	if got := shown(ps); got != "hf|1|wb hf|2|wb hf|3|wb hf|4|wb hf|5|wb" {
		t.Fatalf("start: %s", got)
	}
	changes := 0
	ps.OnChange = func(*PageSet) { changes++ }
	ps.RemoveHeader(2, RemoveThisPage)
	ps.RemoveFooter(2, RemoveForward)
	ps.RemovePageNumber(2, RemoveThisPage)
	ps.RemoveWatermark(3, RemoveForward)
	ps.RemoveBackground(1, RemoveThisPage)
	// The header is off on page 3 only; the footer from page 3 on; the number
	// skips page 3 but the count goes on; the watermark is off from page 4;
	// the background from page 2 only.
	want := "hf|1|wb hf|2|w- --|_|wb h-|4|-b h-|5|-b"
	if got := shown(ps); got != want {
		t.Errorf("after removals:\n got %s\nwant %s", got, want)
	}
	if changes != 5 {
		t.Errorf("OnChange calls = %d", changes)
	}
	for name, remove := range map[string]func(*PageSet, int, RemoveScope){
		"header": (*PageSet).RemoveHeader, "footer": (*PageSet).RemoveFooter, "number": (*PageSet).RemovePageNumber,
		"watermark": (*PageSet).RemoveWatermark, "background": (*PageSet).RemoveBackground,
	} {
		ps := mk()
		remove(ps, 3, RemoveAll)
		got := shown(ps)
		if gone := strings.Count(got, "-") + strings.Count(got, "_"); gone < 5 {
			t.Errorf("%s from all pages: %s", name, got)
		}
		remove(ps, -1, RemoveAll) // out of range: ignored
		remove(ps, 9, RemoveThisPage)
	}
	ps = mk()
	ps.RemoveHeader(0, RemoveForward)
	if ps.ResolvedHeader(0) != nil || ps.ResolvedHeader(4) != nil {
		t.Error("forward from the first page removes everywhere")
	}
	// Removing a page's own number when the next page has its own leaves that alone.
	ps = mk()
	ps.Pages[2].PageNumber = &PageNumberConfig{Show: true, Start: 10, Format: "{n}"}
	ps.RemovePageNumber(1, RemoveThisPage)
	if ps.PageNumberText(1) != "" || ps.PageNumberText(2) != "10" {
		t.Errorf("a later page's own setting stays: %q %q", ps.PageNumberText(1), ps.PageNumberText(2))
	}
}

func TestPageFurnitureCommands(t *testing.T) {
	ps := sheets(3)
	ps.Pages[0].Header = &HeaderFooter{Text: "H"}
	ps.Pages[0].Watermark = &Watermark{Text: "OLD"}
	p := &fakePainter{}
	f := NewTextFlow("", 0, 0, 0, 0)
	f.Pages = ps
	f.Items = []Item{Chars{Text: "page one"}, Break{Kind: BreakPage}, Chars{Text: "two"}, Break{Kind: BreakPage}, Chars{Text: "three"}}
	f.SetPainter(p)
	f.SetMode(ModeEdit)
	st := NewStage(p, 400, 600, ps, f)
	x1, y1, _, _ := ps.ContentRect(1)
	f.MouseDown(x1+4, y1+5) // the caret on the second sheet
	f.MouseUp(0, 0)
	if f.CaretPage() != 1 {
		t.Fatalf("caret page = %d", f.CaretPage())
	}
	if !st.RunCommand(CmdPageRemove("header", RemoveThisPage)) || ps.ResolvedHeader(1) != nil || ps.ResolvedHeader(2) == nil || ps.ResolvedHeader(0) == nil {
		t.Errorf("header removed from the caret's page only: %v %v %v", ps.ResolvedHeader(0), ps.ResolvedHeader(1), ps.ResolvedHeader(2))
	}
	st.RunCommand(CmdPageRemove("watermark", RemoveForward))
	if ps.ResolvedWatermark(0) == nil || ps.ResolvedWatermark(1) != nil {
		t.Error("watermark removed from the caret's page on")
	}
	// Setting a watermark: prompts, applies from the caret's page, keeps the look.
	asked := ""
	answer := "DRAFT"
	st.Prompt = func(msg, initial string) (string, bool) { asked = initial; return answer, true }
	st.RunCommand(CmdSetWatermark)
	if ps.ResolvedWatermark(1) == nil || ps.ResolvedWatermark(1).Text != "DRAFT" || ps.ResolvedWatermark(2).Text != "DRAFT" || ps.ResolvedWatermark(0).Text != "OLD" {
		t.Errorf("watermark from page 2: %+v %+v", ps.ResolvedWatermark(0), ps.ResolvedWatermark(1))
	}
	if asked != "" {
		t.Errorf("the prompt starts empty where there is none: %q", asked)
	}
	st.RunCommand(CmdSetWatermark)
	if asked != "DRAFT" {
		t.Errorf("the prompt shows the current text: %q", asked)
	}
	answer = ""
	st.RunCommand(CmdSetWatermark)
	if ps.ResolvedWatermark(1) != nil {
		t.Error("an empty answer removes it")
	}
	st.Prompt = func(string, string) (string, bool) { return "", false }
	if st.RunCommand(CmdSetWatermark) {
		t.Error("cancel")
	}
	st.Prompt = nil
	if st.RunCommand(CmdSetWatermark) {
		t.Error("no prompt")
	}
	// Spread toggles.
	st.RunCommand(CmdSpread)
	if !ps.Spread || ps.SpreadBook {
		t.Error("spread on")
	}
	st.RunCommand(CmdSpreadBook)
	if !ps.Spread || !ps.SpreadBook {
		t.Error("book spread")
	}
	st.RunCommand(CmdSpreadBook)
	st.RunCommand(CmdSpread)
	if ps.Spread {
		t.Error("toggles off")
	}
	// A flow with no pages, or a box, declines.
	plain, _ := editFlow(t, 0, Chars{Text: "x"})
	pst := NewStage(&fakePainter{}, 10, 10, plain)
	if pst.RunCommand(CmdSpread) || pst.RunCommand(CmdPageRemove("header", RemoveAll)) {
		t.Error("no PageSet: nothing to do")
	}
	bst, _ := stageWith(t, "box")
	if bst.RunCommand(CmdSpread) {
		t.Error("a box has no pages")
	}
}

func TestRemoveMenuInTheHeaderBadge(t *testing.T) {
	ps := docPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "Report"}
	ps.Pages[1].Header = &HeaderFooter{Text: "Own"}
	ps.DoubleClick(headerPoint(1))
	_, _, buttons := ps.badge()
	var remove *badgeButton
	for i := range buttons {
		if buttons[i].label == "Remove ▾" {
			remove = &buttons[i]
		}
	}
	if remove == nil {
		t.Fatalf("buttons = %v", buttons)
	}
	remove.do()
	labels := ps.Menu().Labels()
	if len(labels) != 3 || labels[0] != "Remove header from this page only" || labels[2] != "Remove header from all pages" {
		t.Fatalf("menu = %v", labels)
	}
	ps.Menu().Activate(0)
	if ps.ResolvedHeader(1) != nil || ps.ResolvedHeader(0) == nil {
		t.Errorf("this page only: %v %v", ps.ResolvedHeader(0), ps.ResolvedHeader(1))
	}
	if ps.edit != nil {
		t.Error("editing ends")
	}
	// The page-number menu has the same choices.
	ps.Pages[0].PageNumber = &PageNumberConfig{Show: true, Start: 1}
	ps.selectNumber(1, [4]float64{10, 10, 8, 16})
	labels = ps.Menu().Labels()
	if labels[0] != "Hide page number" || labels[1] != "Hide on this page only" || labels[2] != "Remove from all pages" {
		t.Fatalf("number menu = %v", labels)
	}
	ps.Menu().Activate(1)
	if ps.PageNumberText(1) != "" || ps.PageNumberText(2) == "" {
		t.Errorf("number removed on page 2 only: %q %q", ps.PageNumberText(1), ps.PageNumberText(2))
	}
	ps.selectNumber(2, [4]float64{10, 10, 8, 16})
	ps.Menu().Activate(2)
	if ps.PageNumberText(0) != "" || ps.PageNumberText(2) != "" {
		t.Error("removed from all pages")
	}
}

func TestRuleCommand(t *testing.T) {
	f, st := editFlow(t, 2, Chars{Text: "abcd"})
	if !st.HandleChord("Mod+Alt+Enter") {
		t.Fatal("rule not inserted")
	}
	var rules int
	for _, it := range f.ContentItems() {
		if in, ok := it.(Inline); ok {
			if _, isRule := in.Object.(*Rule); isRule && in.Block {
				rules++
			}
		}
	}
	if rules != 1 || f.EditText() != "ab"+string(objectRune)+"\ncd" {
		t.Fatalf("items = %+v text %q", f.ContentItems(), f.EditText())
	}
	if f.Cursors()[0].Caret != 4 {
		t.Errorf("the caret goes after the rule and its line break: %v", f.Cursors())
	}
	// Two cursors, two separate rules.
	f.SetCursors([]Cursor{At(0), At(6)})
	st.HandleChord("Mod+Alt+Enter")
	var objs []InlineObject
	for _, it := range f.ContentItems() {
		if in, ok := it.(Inline); ok {
			objs = append(objs, in.Object)
		}
	}
	if len(objs) != 3 || objs[0] == objs[1] || objs[1] == objs[2] {
		t.Errorf("each rule is its own object: %v", objs)
	}
	bst, _ := stageWith(t, "box")
	if bst.HandleChord("Mod+Alt+Enter") {
		t.Error("a box cannot hold a rule")
	}
}

func TestFloatingTextOpacityAndFadeButton(t *testing.T) {
	ft, p := newFloat("hello")
	ft.Opacity = 0.5
	ft.Draw()
	i, j := -1, -1
	for k, c := range p.calls {
		if c == "alpha 0.5" && i < 0 {
			i = k
		}
		if c == "alpha 1" {
			j = k
		}
	}
	if i < 0 || j < i {
		t.Fatalf("drawn at half opacity, then restored: %v", p.calls)
	}
	ft.Opacity = 0
	p.calls = nil
	ft.Draw()
	if p.has("alpha 0.5") {
		t.Error("opaque by default")
	}
	var seen []float64
	for range 5 {
		ft.CycleOpacity()
		seen = append(seen, ft.Opacity)
	}
	if want := []float64{0.75, 0.5, 0.25, 0, 0.75}; len(seen) != 5 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] || seen[3] != want[3] || seen[4] != want[4] {
		t.Errorf("cycle = %v", seen)
	}
	ft.Opacity = 0
	r := ft.buttonRect(8)
	ft.MouseDown(r[0]+2, r[1]+2)
	ft.MouseUp(0, 0)
	if ft.Opacity != 0.75 {
		t.Errorf("the Fade button cycles opacity: %g", ft.Opacity)
	}
}

func TestSpreadDrawsTheFoldShadeAndTurnTabs(t *testing.T) {
	ps := sheets(5)
	ps.Spread, ps.Gutter = true, 4
	fp := &fakePainter{}
	ps.Painter = fp
	ps.Draw()
	if !fp.has("fillStyle rgba(0, 0, 0, 0.220)") {
		t.Errorf("the fold is shaded strongest at the spine: %v", fp.calls)
	}
	// Not a spread: no shade.
	ps.Spread = false
	fp2 := &fakePainter{}
	ps.Painter = fp2
	ps.Draw()
	if fp2.has("fillStyle rgba(0, 0, 0, 0.220)") {
		t.Error("a single column has no fold")
	}

	ps.Spread = true
	var got []int
	ps.OnTurn = func(page int) { got = append(got, page) }
	// Rows: [0 1] [2 3] [4]. Sheet 1 is the right of the first pair: its tab turns forward to sheet 2.
	x, y, left, ok := ps.turnTab(1)
	if !ok || left {
		t.Fatalf("right sheet tab: %v %v", left, ok)
	}
	if !ps.MouseDown(x+3, y+3) || len(got) != 1 || got[0] != 2 {
		t.Fatalf("turning forward: %v", got)
	}
	// Sheet 2 is the left of the second pair: back to sheet 0.
	x, y, left, ok = ps.turnTab(2)
	if !ok || !left {
		t.Fatalf("left sheet tab: %v %v", left, ok)
	}
	ps.MouseDown(x+3, y+3)
	if len(got) != 2 || got[1] != 0 {
		t.Fatalf("turning back: %v", got)
	}
	// The first pair has nothing before it; the last sheet nothing after.
	if _, _, _, ok := ps.turnTab(0); ok {
		t.Error("nothing before the first pair")
	}
	if _, can := ps.TurnTarget(4, false); can {
		t.Error("nothing after the last")
	}
	// Without a host there are no tabs.
	ps.OnTurn = nil
	if _, _, _, ok := ps.turnTab(1); ok {
		t.Error("no OnTurn, no tabs")
	}
}
