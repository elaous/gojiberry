package canvas

import (
	"strings"
	"testing"
)

func twoPages() *PageSet {
	ps := NewPageSet(
		Page{Width: 200, Height: 100, Margin: Insets{10, 10, 10, 10}},
		Page{Width: 200, Height: 100, Margin: Insets{10, 10, 10, 10}},
	)
	ps.SetPainter(&fakePainter{})
	return ps
}

func TestPageGeometry(t *testing.T) {
	ps := twoPages() // Pad 24, Gap 24
	if x, y, w, h := ps.PageRect(0); x != 24 || y != 24 || w != 200 || h != 100 {
		t.Fatalf("page 0 = %g %g %g %g", x, y, w, h)
	}
	if _, y, _, _ := ps.PageRect(1); y != 24+100+24 {
		t.Fatalf("page 1 y = %g", y)
	}
	if x, y, w, h := ps.ContentRect(1); x != 34 || y != 158 || w != 180 || h != 80 {
		t.Fatalf("content 1 = %g %g %g %g", x, y, w, h)
	}
	if w, h := ps.Size(); w != 248 || h != 24+100+24+100+24 {
		t.Fatalf("size = %g x %g", w, h)
	}
}

func TestPagesCenterInWiderContainer(t *testing.T) {
	ps := twoPages()
	ps.Width = 400
	if x, _, _, _ := ps.PageRect(0); x != 100 {
		t.Fatalf("x = %g, want 100 (centered)", x)
	}
	if w, _ := ps.Size(); w != 400 {
		t.Fatalf("width = %g", w)
	}
}

func TestPageAt(t *testing.T) {
	ps := twoPages()
	for _, tt := range []struct {
		y    float64
		want int
	}{{10, -1}, {24, 0}, {123, 0}, {130, -1}, {148, 1}, {248, -1}} {
		if got := ps.PageAt(tt.y); got != tt.want {
			t.Errorf("PageAt(%g) = %d, want %d", tt.y, got, tt.want)
		}
	}
}

func TestHeaderFooterInheritance(t *testing.T) {
	h1 := &HeaderFooter{Text: "Report"}
	f1 := &HeaderFooter{Text: "Confidential"}
	ps := NewPageSet(
		Page{Width: 200, Height: 100},
		Page{Width: 200, Height: 100, Header: h1, Footer: f1},
		Page{Width: 200, Height: 100},
		Page{Width: 200, Height: 100, Header: &HeaderFooter{}},
		Page{Width: 200, Height: 100},
	)
	if ps.ResolvedHeader(0) != nil || ps.ResolvedFooter(0) != nil {
		t.Fatal("first page has none unless it sets one")
	}
	if ps.ResolvedHeader(2) != h1 || ps.ResolvedFooter(2) != f1 {
		t.Fatal("pages inherit the previous page's header and footer")
	}
	if ps.ResolvedHeader(3) != nil || ps.ResolvedHeader(4) != nil {
		t.Fatal("an empty header removes it from that page on")
	}
	if ps.ResolvedFooter(4) != f1 {
		t.Fatal("the footer is unaffected")
	}
}

func TestPageNumberConfigInheritsAndRestarts(t *testing.T) {
	ps := NewPageSet(
		Page{Width: 200, Height: 100},
		Page{Width: 200, Height: 100, PageNumber: &PageNumberConfig{Show: true, Start: 5, Format: "Page {n}"}},
		Page{Width: 200, Height: 100},
		Page{Width: 200, Height: 100, PageNumber: &PageNumberConfig{Show: true, Start: 1, Style: NumLowerRoman}},
		Page{Width: 200, Height: 100},
		Page{Width: 200, Height: 100, PageNumber: &PageNumberConfig{Show: false}},
		Page{Width: 200, Height: 100},
	)
	want := []string{"", "Page 5", "Page 6", "i", "ii", "", ""}
	for i, w := range want {
		if got := ps.PageNumberText(i); got != w {
			t.Errorf("page %d number = %q, want %q", i, got, w)
		}
	}
	if cfg, n := ps.ResolvedPageNumber(2); cfg == nil || n != 6 {
		t.Fatalf("page 2 resolved = %v, %d", cfg, n)
	}
}

func TestPageNumberStyles(t *testing.T) {
	tests := []struct {
		n     int
		style PageNumberStyle
		want  string
	}{
		{7, NumArabic, "7"}, {4, NumLowerRoman, "iv"}, {1994, NumUpperRoman, "MCMXCIV"},
		{1, NumLowerAlpha, "a"}, {26, NumLowerAlpha, "z"}, {27, NumLowerAlpha, "aa"}, {28, NumUpperAlpha, "AB"},
		{0, NumLowerRoman, "0"}, {5000, NumUpperRoman, "5000"}, {0, NumUpperAlpha, "0"},
	}
	for _, tt := range tests {
		if got := formatPageNumber(tt.n, tt.style); got != tt.want {
			t.Errorf("formatPageNumber(%d, %v) = %q, want %q", tt.n, tt.style, got, tt.want)
		}
	}
}

func TestTallHeaderAndFooterShrinkTheContentArea(t *testing.T) {
	ps := NewPageSet(Page{
		Width: 200, Height: 200, Margin: Insets{10, 20, 10, 20},
		Header:     &HeaderFooter{Text: "a\nb\nc", Style: TextStyle{FontSize: 12, LineHeight: 20}, Offset: 5}, // 60px tall at 5
		PageNumber: &PageNumberConfig{Show: true, Start: 1, TextStyle: TextStyle{FontSize: 12, LineHeight: 20}},
	})
	_, y, _, h := ps.ContentRect(0)
	if y != 24+71 || h != 200-71-30 {
		t.Fatalf("content y=%g h=%g", y, h)
	}
}

func TestPageSetDrawViewVersusEdit(t *testing.T) {
	ps := twoPages()
	ps.Pages[0].Header = &HeaderFooter{Text: "Title", Align: AlignRight}
	ps.Pages[0].PageNumber = &PageNumberConfig{Show: true, Start: 1, Format: "Page {n}", Align: AlignCenter}
	p := ps.Painter.(*fakePainter)
	ps.Draw()
	for _, want := range []string{
		"fillStyle #e8eaed", "fillStyle #ffffff", "strokeStyle #c4c7cd",
		`fillText "Title" 174 `, // right edge 24+200-10, minus 5 runes*8
		`fillText "Page 1"`, `fillText "Page 2"`,
	} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	if p.has("strokeStyle #b9a9ff") {
		t.Fatal("view mode draws no margin guides")
	}
	ps.SetMode(ModeEdit)
	if ps.SetMode(ModeEdit) {
		t.Fatal("same mode is a no-op")
	}
	p.calls = nil
	ps.Draw()
	if !p.has("strokeStyle #b9a9ff") {
		t.Fatal("edit mode draws margin guides")
	}
}

func TestHeaderPartsAreDrawnLeftCentreAndRight(t *testing.T) {
	p := &fakePainter{}
	ps := NewPageSet(Page{Width: 200, Height: 100, Margin: Insets{20, 10, 20, 10}, Header: &HeaderFooter{Parts: [3]string{"L", "CC", "RRR"}}})
	ps.SetPainter(p)
	ps.Draw()
	var got []string
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillText") {
			got = append(got, c)
		}
	}
	want := []string{`fillText "L" 44 `, `fillText "CC" 116 `, `fillText "RRR" 180 `}
	if len(got) != 3 {
		t.Fatalf("texts: %v", got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("part %d = %s, want it to start %s", i, got[i], w)
		}
	}
	if !(&HeaderFooter{Parts: [3]string{"", "x", ""}}).HasParts() || (&HeaderFooter{Text: "t"}).HasParts() {
		t.Error("HasParts")
	}
}
