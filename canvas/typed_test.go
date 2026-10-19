package canvas

import (
	"fmt"
	"strings"
	"testing"
)

func TestTypedRangesAreDrawnInThePeersColour(t *testing.T) {
	f, p := newFlow("hello world", 400, 200)
	f.TypedRanges = []TypedRange{{From: 5, To: 8, Name: "Ana", Color: "#ff0000"}}
	f.Draw()
	piece := -1
	for i, c := range p.calls {
		if len(c) > 15 && c[:15] == `fillText " wo" ` {
			piece = i
		}
	}
	if piece < 1 {
		t.Fatalf("the run was not split at the typed range: %q", p.calls)
	}
	if p.calls[piece-1] != "fillStyle #ff0000" {
		t.Errorf("the typed piece is not in Ana's colour: %q", p.calls[piece-1])
	}
	if !p.has(`fillText "hello"`) || !p.has(`fillText "rld"`) {
		t.Errorf("the rest is drawn too: %q", p.calls)
	}
	f.TypedRanges = nil
	p.calls = nil
	f.Draw()
	if !p.has(`fillText "hello world"`) {
		t.Errorf("not drawn whole: %q", p.calls)
	}
}

// Right to left text is drawn with the first characters at the right end: what was typed is coloured where it stands.
func TestTypedRangesInRightToLeftText(t *testing.T) {
	f, p := newFlow("אבגד", 400, 200)
	f.TypedRanges = []TypedRange{{From: 0, To: 2, Name: "Ana", Color: "#ff0000"}}
	lay := f.Layout()
	r := lay.Lines[0].Runs[0]
	if r.Level%2 != 1 {
		t.Skipf("the run is not right to left: level %d", r.Level)
	}
	f.Draw()
	x0 := lay.Lines[0].X + r.X
	var typedAt, restAt float64 = -1, -1
	for i, c := range p.calls {
		var s string
		var x, y float64
		if n, _ := fmt.Sscanf(c, "fillText %q %g %g", &s, &x, &y); n == 3 {
			switch s {
			case "אב":
				typedAt = x
				if p.calls[i-1] != "fillStyle #ff0000" {
					t.Errorf("the typed piece is not in Ana's colour: %q", p.calls[i-1])
				}
			case "גד":
				restAt = x
			}
		}
	}
	// the first two characters are the rightmost 16px of the 32px run
	if typedAt != x0+16 || restAt != x0 {
		t.Errorf("pieces at %v and %v, want %v and %v", typedAt, restAt, x0+16, x0)
	}
}

// In vertical text the characters stand along the column: what was typed is coloured there too.
func TestTypedRangesInVerticalText(t *testing.T) {
	f, p := verticalFlow("abcdef")
	f.TypedRanges = []TypedRange{{From: 2, To: 4, Name: "Ana", Color: "#ff0000"}}
	f.Draw()
	seen := map[string]string{}
	last := ""
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillStyle ") {
			last = c
		}
		var s string
		var x, y float64
		if n, _ := fmt.Sscanf(c, "fillText %q %g %g", &s, &x, &y); n == 3 && (s == "ab" || s == "cd" || s == "ef") {
			seen[s] = last
		}
	}
	if seen["cd"] != "fillStyle #ff0000" || seen["ab"] == "fillStyle #ff0000" || seen["ef"] == "fillStyle #ff0000" {
		t.Errorf("the pieces and their colours: %v", seen)
	}
}
