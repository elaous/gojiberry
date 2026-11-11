package buffer

import "testing"

func naiveInsert(buf []rune, pos int, r rune) []rune {
	out := make([]rune, 0, len(buf)+1)
	out = append(out, buf[:pos]...)
	out = append(out, r)
	out = append(out, buf[pos:]...)
	return out
}

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
