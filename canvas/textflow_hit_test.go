package canvas

import "testing"

// CaretRects answers for many offsets what CaretRect answers for one, from a single layout.
func TestCaretRectsMatchCaretRect(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: "alpha beta gamma\ndelta epsilon zeta\neta theta iota kappa lambda mu nu xi omicron pi rho"})
	offsets := []int{0, 3, 16, 17, 30, 36, 50, 200}
	got := f.CaretRects(offsets)
	for i, o := range offsets {
		x, y, h, ok := f.CaretRect(o)
		if got[i].X != x || got[i].Y != y || got[i].H != h || got[i].OK != ok {
			t.Errorf("offset %d: CaretRects %+v, CaretRect %v %v %v %v", o, got[i], x, y, h, ok)
		}
	}
}
