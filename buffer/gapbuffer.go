// Package buffer provides GapBuffer, the text-editing data structure
// classic Emacs (among others) uses: a mutable, indexable sequence
// supporting O(1) amortized Insert/Delete at a moving edit point,
// paying an O(distance) memmove only when that point jumps somewhere
// new. This exists because a plain []rune buffer -- what
// examples/canvas-editor-app started with -- costs an O(n) shift on
// *every single* insert or delete regardless of where it happens (Go's
// append(a[:i], append(new, a[i:]...)...) always copies the entire
// tail), which is fine for a demo-sized document but not the "500+
// pages without dropping below 60fps" a real one needs: at that scale,
// every keystroke re-copying hundreds of thousands of elements adds up
// fast. A gap buffer keeps that same simple "flat array" mental model
// (still trivially rendered/measured/word-wrapped exactly like a plain
// slice) while making the common case -- typing, or navigating character
// by character -- cheap.
//
// A rope (a balanced tree of chunks) or piece table (an immutable
// original buffer plus a list of edit-buffer references) would give
// better worst-case behavior for edits that jump to a wildly different
// position on every single operation. That's not the actual access
// pattern real editing sessions have -- typing is local, and even
// clicking somewhere new only pays the relocation cost once, not per
// keystroke after that -- so the simpler, easier-to-get-right structure
// was chosen deliberately, not for lack of awareness of the
// alternatives.
package buffer

// GapBuffer is a mutable sequence of T. The zero value is an empty,
// ready-to-use buffer.
type GapBuffer[T any] struct {
	buf   []T
	start int // buf[:start] is the live span before the gap
	end   int // buf[end:] is the live span after the gap; buf[start:end] is unused capacity
}

// FromSlice returns a GapBuffer containing a copy of items, in order.
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

// At returns the live element at index i (0 <= i < Len()). Always
// O(1), regardless of the gap's current position -- only Insert/
// Delete ever need to move it.
func (g *GapBuffer[T]) At(i int) T {
	if i < g.start {
		return g.buf[i]
	}
	return g.buf[i+(g.end-g.start)]
}

// Set overwrites the live element at index i (0 <= i < Len()).
func (g *GapBuffer[T]) Set(i int, v T) {
	if i < g.start {
		g.buf[i] = v
	} else {
		g.buf[i+(g.end-g.start)] = v
	}
}

// InsertAt inserts items starting at index pos (0 <= pos <= Len()),
// shifting everything at/after pos to make room. O(1) amortized when
// pos == the last edit position (typing, or deleting character by
// character); O(|pos - last edit position|) once, the next time pos
// lands somewhere new.
func (g *GapBuffer[T]) InsertAt(pos int, items ...T) {
	if len(items) == 0 {
		return
	}
	g.moveGapTo(pos)
	g.growGap(len(items))
	copy(g.buf[g.start:], items)
	g.start += len(items)
}

// DeleteAt removes the n live elements starting at index pos (0 <=
// pos, pos+n <= Len()). Same amortized cost as InsertAt.
func (g *GapBuffer[T]) DeleteAt(pos, n int) {
	if n == 0 {
		return
	}
	g.moveGapTo(pos)
	g.end += n
}

// Slice returns a plain, freshly-allocated []T copy of the live
// elements in [lo, hi) -- for rendering, search, or anything else that
// needs a real contiguous view (word-wrap measurement, serialization).
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

// moveGapTo relocates the gap so it starts at index pos. A no-op when
// the gap is already there (pos == g.start) -- the case every
// subsequent same-neighborhood edit hits, which is what makes those
// edits O(1) instead of paying this move every time.
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

// growGap ensures at least n free slots between the two live spans,
// reallocating (and doubling capacity, the same amortized-O(1)
// argument append itself relies on) only when the current gap is too
// small. Rebuilds via a fresh slice rather than in-place pointer
// arithmetic -- simpler to get right, at the cost of an O(live)
// rebuild exactly when append's own growth would also cost O(live).
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
