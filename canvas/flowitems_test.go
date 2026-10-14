package canvas

import (
	"fmt"
	"strings"
	"testing"
)

func TestTextScaleMakesAllTheTextLargerWhateverItsSizes(t *testing.T) {
	f, _ := selFlow(Chars{Text: "plain\n"}, Chars{Text: "sized", Format: Format{FontSize: 20}})
	base := f.Layout()
	f.TextScale = 2
	big := f.Layout()
	if len(base.Lines) < 2 || len(big.Lines) < 2 {
		t.Fatalf("lines: %d %d", len(base.Lines), len(big.Lines))
	}
	for i := 0; i < 2; i++ {
		if got, want := big.Lines[i].H, 2*base.Lines[i].H; got < want-0.5 || got > want+0.5 {
			t.Errorf("line %d is %.1f high at scale 2, want %.1f", i, got, want)
		}
	}
	f.TextScale = 1
	if again := f.Layout(); again.Lines[0].H != base.Lines[0].H {
		t.Errorf("scale 1 is not as it was: %.1f, was %.1f", again.Lines[0].H, base.Lines[0].H)
	}
}

func TestPeerPaletteGivesEachPersonOneOfTheColours(t *testing.T) {
	f, _ := selFlow(Chars{Text: "hello world"})
	f.RemoteCursors = []RemoteCursor{{Name: "Cy", Color: "#ff0000"}, {Name: "Ana", Color: "#ff0001"}, {Name: "Bo", Color: "#ff0002"}, {Name: "Ana", Color: "#ff0001", Anchor: 1, Caret: 3}}
	if got := f.peerColour(f.RemoteCursors[0]); got != "#ff0000" {
		t.Errorf("without a palette a person's own colour is used: %s", got)
	}
	f.PeerPalette = ColourBlindPalette
	colours := map[string]string{}
	for _, c := range f.RemoteCursors {
		got := f.peerColour(c)
		if prev, ok := colours[c.Name]; ok && prev != got {
			t.Errorf("%s changes colour between cursors: %s, %s", c.Name, prev, got)
		}
		colours[c.Name] = got
	}
	if colours["Ana"] != ColourBlindPalette[0] || colours["Bo"] != ColourBlindPalette[1] || colours["Cy"] != ColourBlindPalette[2] {
		t.Errorf("colours by order of name: %v", colours)
	}
	if colours["Ana"] == colours["Bo"] || colours["Bo"] == colours["Cy"] {
		t.Errorf("two people share a colour: %v", colours)
	}
}

func TestStrongCaretIsThickerAndTheSelectionStronger(t *testing.T) {
	width := func(strong bool) (caretW float64, sel string) {
		f, p := selFlow(Chars{Text: "hello world"})
		f.SetMode(ModeEdit)
		NewStage(p, 400, 400, f)
		f.MouseDown(10+8*2, 40)
		f.MouseUp(0, 0)
		f.StrongCaret = strong
		f.SetCaretVisible(true)
		p.calls = nil
		f.drawCarets()
		for _, c := range p.calls {
			var x, y, w, h float64
			if n, _ := fmt.Sscanf(c, "fillRect %g %g %g %g", &x, &y, &w, &h); n == 4 && w > caretW {
				caretW = w
			}
		}
		f.SetSelection(0, 5)
		lay := f.Layout()
		p.calls = nil
		f.drawSelection(lay)
		for _, c := range p.calls {
			if strings.HasPrefix(c, "fillStyle") {
				sel = c // the tint in force when the first rectangle is filled
			}
			if strings.HasPrefix(c, "fillRect") {
				break
			}
		}
		return
	}
	thin, thinSel := width(false)
	thick, thickSel := width(true)
	if !(thick > thin) {
		t.Errorf("the strong caret is %g wide, the default %g", thick, thin)
	}
	if thinSel == thickSel || !strings.Contains(thickSel, StrongSelectColor) {
		t.Errorf("selection tints: default %q, strong %q", thinSel, thickSel)
	}
}
