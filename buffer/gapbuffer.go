package buffer

type GapBuffer[T any] struct {
	buf   []T
	start int // buf[:start] is the live span before the gap
	end   int // buf[end:] is the live span after the gap.
}

func FromSlice[T any](items []T) *GapBuffer[T] {
	g := &GapBuffer[T]{buf: append([]T(nil), items...)}
	g.start = len(g.buf)
	g.end = len(g.buf)
	return g
}

// Len returns the number of live elements.
func (g *GapBuffer[T]) Len() int {
	return len(g.buf) - (g.end - g.start)
}

// At returns element i in O(1), wherever the gap is.
func (g *GapBuffer[T]) At(i int) T {
	if i < g.start {
		return g.buf[i]
	}
	return g.buf[i+(g.end-g.start)]
}

func (g *GapBuffer[T]) Set(i int, v T) {
	if i < g.start {
		g.buf[i] = v
	} else {
		g.buf[i+(g.end-g.start)] = v
	}
}

func (g *GapBuffer[T]) InsertAt(pos int, items ...T) {
	if len(items) == 0 {
		return
	}
	g.moveGapTo(pos)
	g.growGap(len(items))
	copy(g.buf[g.start:], items)
	g.start += len(items)
}

func (g *GapBuffer[T]) DeleteAt(pos, n int) {
	if n == 0 {
		return
	}
	g.moveGapTo(pos)
	g.end += n
}

func (g *GapBuffer[T]) Slice(lo, hi int) []T {
	out := make([]T, 0, hi-lo)
	if lo < g.start {
		upper := hi
		if upper > g.start {
			upper = g.start
		}
		out = append(out, g.buf[lo:upper]...)
	}
	if hi > g.start {
		lower := lo
		if lower < g.start {
			lower = g.start
		}
		out = append(out, g.buf[lower+(g.end-g.start):hi+(g.end-g.start)]...)
	}
	return out
}

// ToSlice returns every live element, in order.
func (g *GapBuffer[T]) ToSlice() []T {
	return g.Slice(0, g.Len())
}

func (g *GapBuffer[T]) moveGapTo(pos int) {
	switch {
	case pos < g.start:
		n := g.start - pos
		copy(g.buf[g.end-n:g.end], g.buf[pos:g.start])
		g.start, g.end = pos, g.end-n
	case pos > g.start:
		n := pos - g.start
		copy(g.buf[g.start:g.start+n], g.buf[g.end:g.end+n])
		g.start, g.end = g.start+n, g.end+n
	}
}

func (g *GapBuffer[T]) growGap(n int) {
	if g.end-g.start >= n {
		return
	}
	live := g.Len()
	newCap := (live+n)*2 + 1
	if newCap < 16 {
		newCap = 16
	}
	newBuf := make([]T, newCap)
	copy(newBuf, g.buf[:g.start])
	tailLen := len(g.buf) - g.end
	copy(newBuf[newCap-tailLen:], g.buf[g.end:])
	g.buf = newBuf
	g.end = newCap - tailLen
}
