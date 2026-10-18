package canvas

import "testing"

func TestABusyObjectHasAnOutlineAndABadge(t *testing.T) {
	f, p := newFlow("", 300, 200)
	rule := &Rule{Color: "#000", Thickness: 2}
	f.Items = []Item{Chars{Text: "x\n"}, Inline{Object: rule, Block: true}, Chars{Text: "y"}}
	f.ObjectBusy = map[InlineObject]Busy{rule: {Name: "Ana", Color: "#2563eb"}}
	f.Draw()
	if !p.has("strokeStyle #2563eb") || !p.has(`fillText "busy"`) || !p.has("fillStyle #2563eb") {
		t.Fatalf("outline and badge: %v", p.calls)
	}
	p.calls = nil
	f.ObjectBusy = map[InlineObject]Busy{rule: {Name: "Me", Color: "#e11d48", Mine: true}}
	f.Draw()
	if !p.has("strokeStyle #e11d48") || p.has(`fillText "busy"`) {
		t.Fatalf("my own object has the outline only: %v", p.calls)
	}
	p.calls = nil
	f.ObjectBusy = map[InlineObject]Busy{rule: {Name: "X", Color: "red; evil"}}
	f.Draw()
	if !p.has("strokeStyle #6b7280") || p.has("evil") {
		t.Fatalf("a colour that is not hex is drawn grey: %v", p.calls)
	}
	if readableOn("#facc15") != "#000000" || readableOn("#1e3a8a") != "#ffffff" || readableOn("#fff") != "#000000" {
		t.Fatal("text colour on the badge")
	}
}
