package canvas

import "unicode"

// Bidi: paragraph direction and per-character levels, per the Unicode algorithm.

type bidiClass uint8

const (
	bcNeutral bidiClass = iota // space, punctuation, symbols
	bcL                        // strong left-to-right
	bcR                        // strong right-to-left (Hebrew, ...)
	bcAL                       // strong right-to-left (Arabic, ...)
	bcEN                       // European number
	bcAN                       // Arabic-Indic number
	bcMark                     // a combining mark: takes the class of the letter before it
)

func classOf(r rune) bidiClass {
	switch {
	case r >= '0' && r <= '9', r >= 0x06F0 && r <= 0x06F9:
		return bcEN
	case r >= 0x0660 && r <= 0x0669, r == 0x066B, r == 0x066C:
		return bcAN
	case r == 0x200F: // right-to-left mark
		return bcR
	case r == 0x200E: // left-to-right mark
		return bcL
	case r >= 0x0590 && r <= 0x05FF, r >= 0x07C0 && r <= 0x089F, r >= 0xFB1D && r <= 0xFB4F, r >= 0x10800 && r <= 0x10FFF:
		if unicode.Is(unicode.Mn, r) {
			return bcMark
		}
		return bcR
	case r >= 0x0600 && r <= 0x07BF, r >= 0x08A0 && r <= 0x08FF, r >= 0xFB50 && r <= 0xFDFF, r >= 0xFE70 && r <= 0xFEFF:
		if unicode.Is(unicode.Mn, r) {
			return bcMark
		}
		if unicode.IsLetter(r) {
			return bcAL
		}
		return bcNeutral
	case unicode.Is(unicode.Mn, r):
		return bcMark
	case unicode.IsLetter(r):
		return bcL
	}
	return bcNeutral
}

// IsRTLText reports whether text starts in a right-to-left script.
func IsRTLText(rs []rune) bool {
	for _, r := range rs {
		switch classOf(r) {
		case bcR, bcAL:
			return true
		case bcL:
			return false
		}
	}
	return false
}

// bidiLevels returns each rune's embedding level, or nil if all are level 0.
func bidiLevels(rs []rune, rtl bool) []uint8 {
	cls := make([]bidiClass, len(rs))
	any := false
	for i, r := range rs {
		cls[i] = classOf(r)
		if cls[i] == bcR || cls[i] == bcAL || cls[i] == bcAN {
			any = true
		}
	}
	if !any && !rtl {
		return nil
	}
	base := uint8(0)
	if rtl {
		base = 1
	}
	baseClass := bcL
	if rtl {
		baseClass = bcR
	}
	// Marks follow the letter before them.
	prev := baseClass
	for i, c := range cls {
		if c == bcMark {
			cls[i] = prev
		} else if c != bcNeutral && c != bcEN && c != bcAN {
			prev = c
		}
	}
	// W2: a European number after an Arabic letter is an Arabic number.
	last := baseClass
	for i, c := range cls {
		switch c {
		case bcL, bcR, bcAL:
			last = c
		case bcEN:
			if last == bcAL {
				cls[i] = bcAN
			}
		}
	}
	// W4, W5: separators and signs join adjacent numbers.
	for i := 1; i+1 < len(rs); i++ {
		if cls[i] == bcNeutral && cls[i-1] == cls[i+1] && (cls[i-1] == bcEN || cls[i-1] == bcAN) {
			switch rs[i] {
			case '.', ',', ':', '/', ' ', '٫', '٬':
				cls[i] = cls[i-1]
			}
		}
	}
	for i, r := range rs {
		if cls[i] != bcNeutral {
			continue
		}
		switch r {
		case '%', '$', '#', '+', '-', '°', '€', '£', '¥':
			if (i > 0 && cls[i-1] == bcEN) || (i+1 < len(rs) && cls[i+1] == bcEN) {
				cls[i] = bcEN
			}
		}
	}
	// N1, N2: neutrals between two strong sides of one direction take it.
	strong := func(c bidiClass) (bidiClass, bool) {
		switch c {
		case bcL:
			return bcL, true
		case bcR, bcAL, bcEN, bcAN:
			return bcR, true
		}
		return 0, false
	}
	res := make([]bidiClass, len(cls))
	copy(res, cls)
	for i := 0; i < len(cls); {
		if cls[i] != bcNeutral {
			i++
			continue
		}
		j := i
		for j < len(cls) && cls[j] == bcNeutral {
			j++
		}
		before, after := baseClass, baseClass
		if i > 0 {
			before, _ = strong(cls[i-1])
		}
		if j < len(cls) {
			after, _ = strong(cls[j])
		}
		dir := baseClass
		if before == after {
			dir = before
		}
		for k := i; k < j; k++ {
			res[k] = dir
		}
		i = j
	}
	// I1, I2.
	lv := make([]uint8, len(rs))
	for i, c := range res {
		l := base
		if base%2 == 0 {
			switch c {
			case bcR, bcAL:
				l = base + 1
			case bcEN, bcAN:
				l = base + 2
			}
		} else {
			switch c {
			case bcL, bcEN, bcAN:
				l = base + 1
			}
		}
		lv[i] = l
	}
	return lv
}

// visualOrder returns piece drawing order, as indexes into levels (rule L2).
func visualOrder(levels []uint8) []int {
	order := make([]int, len(levels))
	for i := range order {
		order[i] = i
	}
	var hi, lo uint8 = 0, 255
	for _, l := range levels {
		hi = max(hi, l)
		if l%2 == 1 {
			lo = min(lo, l)
		}
	}
	for lvl := hi; lvl >= lo && lvl > 0; lvl-- {
		for i := 0; i < len(order); {
			if levels[order[i]] < lvl {
				i++
				continue
			}
			j := i
			for j < len(order) && levels[order[j]] >= lvl {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				order[a], order[b] = order[b], order[a]
			}
			i = j
		}
	}
	return order
}
