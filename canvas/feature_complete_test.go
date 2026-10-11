package canvas

import "testing"

func TestChordNamesSpace(t *testing.T) {
	if got := Chord(" ", true, false, false, false); got != "Mod+Space" {
		t.Fatalf("got %q", got)
	}
	if got := Chord("a", true, true, false, false); got != "Mod+Shift+A" {
		t.Fatalf("letters unchanged: %q", got)
	}
}

func TestCompleteFeatureCallsBack(t *testing.T) {
	opened := 0
	r := NewRegistry()
	CompleteFeature{OnOpen: func() { opened++ }}.Install(r)
	if !r.Has(CmdComplete) {
		t.Fatal("command missing")
	}
	if !r.Run(CmdComplete, &CommandContext{}) || opened != 1 {
		t.Fatalf("run: opened %d", opened)
	}
	r2 := NewRegistry()
	CompleteFeature{}.Install(r2)
	if r2.Run(CmdComplete, &CommandContext{}) {
		t.Fatal("no callback: not applied")
	}
}
