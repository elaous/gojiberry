package canvas

import "testing"

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
