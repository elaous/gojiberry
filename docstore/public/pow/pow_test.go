package pow

import "testing"

func TestSolveAndAnswer(t *testing.T) {
	n, ok := Solve("abc", 10, 0)
	if !ok || !Solves("abc", n, 10) {
		t.Fatal("a nonce is found and checks")
	}
	a, ok := Answer("slot.mac 8")
	if !ok || a[:len("slot.mac ")] != "slot.mac " {
		t.Fatalf("answer %q", a)
	}
	for _, bad := range []string{"", "nobits", "x 0", "x 99", "x y", "x 27"} {
		if _, ok := Answer(bad); ok {
			t.Errorf("%q should not be answered", bad)
		}
	}
	if _, ok := Solve("abc", 30, 10); ok {
		t.Error("a small limit gives up")
	}
}
