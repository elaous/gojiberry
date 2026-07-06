package buffer

import "testing"

// naiveInsert mimics the plain-[]rune approach
// examples/canvas-editor-app's document.insert used before this
// package existed: append(a[:i], append(new, a[i:]...)...), an O(n)
// shift on every single call regardless of where i is.
func naiveInsert(buf []rune, pos int, r rune) []rune {
	out := make([]rune, 0, len(buf)+1)
	out = append(out, buf[:pos]...)
	out = append(out, r)
	out = append(out, buf[pos:]...)
	return out
}

// Both benchmarks model the same realistic scenario: a document
// that's already large (100,000 characters -- a modest handful of
// pages), then 1,000 characters typed one at a time in the middle of
// it (position 50,000 growing to 50,999), the way a real editing
// session actually behaves -- not 1,000 inserts at 1,000 unrelated
// random positions, which would defeat the gap buffer's entire
// premise (that edits cluster around a moving-but-locally-stable
// point) and isn't what typing is anyway.
const (
	benchDocSize  = 100_000
	benchTypeSize = 1_000
	benchAt       = 50_000
)

func BenchmarkNaiveSliceInsert_TypingInTheMiddle(b *testing.B) {
	base := make([]rune, benchDocSize)
	for i := range base {
		base[i] = 'x'
	}
	for i := 0; i < b.N; i++ {
		buf := append([]rune(nil), base...)
		pos := benchAt
		for j := 0; j < benchTypeSize; j++ {
			buf = naiveInsert(buf, pos, 'a')
			pos++
		}
	}
}

func BenchmarkGapBufferInsert_TypingInTheMiddle(b *testing.B) {
	base := make([]rune, benchDocSize)
	for i := range base {
		base[i] = 'x'
	}
	for i := 0; i < b.N; i++ {
		g := FromSlice(base)
		pos := benchAt
		for j := 0; j < benchTypeSize; j++ {
			g.InsertAt(pos, 'a')
			pos++
		}
	}
}
