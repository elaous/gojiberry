package buffer

import (
	"math/rand"
	"testing"
)

func TestEmptyBuffer(t *testing.T) {
	g := &GapBuffer[rune]{}
	if g.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", g.Len())
	}
	if got := g.ToSlice(); len(got) != 0 {
		t.Fatalf("ToSlice() = %v, want empty", got)
	}
}

func TestFromSliceAndToSlice(t *testing.T) {
	g := FromSlice([]rune("hello"))
	if g.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", g.Len())
	}
	if got := string(g.ToSlice()); got != "hello" {
		t.Fatalf("ToSlice() = %q, want %q", got, "hello")
	}
}

func TestInsertAtStartMiddleEnd(t *testing.T) {
	g := FromSlice([]rune("helo"))
	g.InsertAt(3, 'l') // "helo" -> "hello"
	if got := string(g.ToSlice()); got != "hello" {
		t.Fatalf("insert in middle: got %q, want %q", got, "hello")
	}

	g2 := FromSlice([]rune("ello"))
	g2.InsertAt(0, 'h')
	if got := string(g2.ToSlice()); got != "hello" {
		t.Fatalf("insert at start: got %q, want %q", got, "hello")
	}

	g3 := FromSlice([]rune("hell"))
	g3.InsertAt(4, 'o')
	if got := string(g3.ToSlice()); got != "hello" {
		t.Fatalf("insert at end: got %q, want %q", got, "hello")
	}
}

func TestDeleteAtStartMiddleEnd(t *testing.T) {
	g := FromSlice([]rune("hxello"))
	g.DeleteAt(1, 1)
	if got := string(g.ToSlice()); got != "hello" {
		t.Fatalf("delete in middle: got %q, want %q", got, "hello")
	}

	g2 := FromSlice([]rune("xhello"))
	g2.DeleteAt(0, 1)
	if got := string(g2.ToSlice()); got != "hello" {
		t.Fatalf("delete at start: got %q, want %q", got, "hello")
	}

	g3 := FromSlice([]rune("hellox"))
	g3.DeleteAt(5, 1)
	if got := string(g3.ToSlice()); got != "hello" {
		t.Fatalf("delete at end: got %q, want %q", got, "hello")
	}
}

func TestGapMovesBothDirectionsRepeatedly(t *testing.T) {
	g := FromSlice([]rune("0123456789"))
	g.InsertAt(5, 'a') // "01234a56789"
	g.InsertAt(2, 'b') // "01b234a56789"
	g.InsertAt(9, 'c') // "01b234a56c789" (index 9 is between '6' and '7')
	g.InsertAt(0, 'd') // "d01b234a56c789"
	g.DeleteAt(3, 1)   // remove 'b' -> "d01234a56c789"
	g.DeleteAt(0, 1)   // remove 'd' -> "01234a56c789"
	want := "01234a56c789"
	if got := string(g.ToSlice()); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInsertMultipleAtOnce(t *testing.T) {
	g := FromSlice([]rune("ho"))
	g.InsertAt(1, 'e', 'l', 'l')
	if got := string(g.ToSlice()); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestAtAndSet(t *testing.T) {
	g := FromSlice([]rune("hello"))
	if g.At(1) != 'e' {
		t.Fatalf("At(1) = %q, want %q", g.At(1), 'e')
	}
	g.Set(1, 'a')
	if got := string(g.ToSlice()); got != "hallo" {
		t.Fatalf("after Set: got %q, want %q", got, "hallo")
	}
	g.InsertAt(0, 'h')
	if got := string(g.ToSlice()); got != "hhallo" {
		t.Fatalf("got %q, want %q", got, "hhallo")
	}
}

func TestSliceRange(t *testing.T) {
	g := FromSlice([]rune("0123456789"))
	g.InsertAt(5, 'x')
	if got := string(g.Slice(2, 8)); got != "234x56" {
		t.Fatalf("Slice straddling the gap: got %q, want %q", got, "234x56")
	}
	if got := string(g.Slice(0, 3)); got != "012" {
		t.Fatalf("Slice entirely before the gap: got %q, want %q", got, "012")
	}
	if got := string(g.Slice(7, 11)); got != "6789" {
		t.Fatalf("Slice entirely after the gap: got %q, want %q", got, "6789")
	}
}

func TestGrowsBeyondInitialCapacity(t *testing.T) {
	g := &GapBuffer[rune]{}
	for i := 0; i < 1000; i++ {
		g.InsertAt(g.Len(), rune('a'+i%26))
	}
	if g.Len() != 1000 {
		t.Fatalf("Len() = %d, want 1000", g.Len())
	}
	for i := 0; i < 1000; i++ {
		if want := rune('a' + i%26); g.At(i) != want {
			t.Fatalf("At(%d) = %q, want %q", i, g.At(i), want)
		}
	}
}

func TestAgainstNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	g := &GapBuffer[rune]{}
	var naive []rune

	for op := 0; op < 5000; op++ {
		switch rng.Intn(3) {
		case 0: // insert
			pos := 0
			if len(naive) > 0 {
				pos = rng.Intn(len(naive) + 1)
			}
			ch := rune('a' + rng.Intn(26))
			g.InsertAt(pos, ch)
			naive = append(naive[:pos], append([]rune{ch}, naive[pos:]...)...)
		case 1: // delete
			if len(naive) == 0 {
				continue
			}
			pos := rng.Intn(len(naive))
			g.DeleteAt(pos, 1)
			naive = append(naive[:pos], naive[pos+1:]...)
		case 2: // read
			if len(naive) == 0 {
				continue
			}
			pos := rng.Intn(len(naive))
			if got, want := g.At(pos), naive[pos]; got != want {
				t.Fatalf("op %d: At(%d) = %q, want %q", op, pos, got, want)
			}
		}
		if g.Len() != len(naive) {
			t.Fatalf("op %d: Len() = %d, want %d", op, g.Len(), len(naive))
		}
	}

	if got, want := string(g.ToSlice()), string(naive); got != want {
		t.Fatalf("final mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCharFormatPayload(t *testing.T) {
	type char struct {
		Rune rune
		Bold bool
	}
	g := FromSlice([]char{{Rune: 'h'}, {Rune: 'i'}})
	g.InsertAt(1, char{Rune: 'e', Bold: true})
	got := g.ToSlice()
	want := []char{{Rune: 'h'}, {Rune: 'e', Bold: true}, {Rune: 'i'}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
