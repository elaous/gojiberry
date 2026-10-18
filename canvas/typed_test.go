package canvas

import "testing"

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
