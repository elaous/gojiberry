// Package fontdata holds the two types that describe a font family and its cuts. It has no dependencies; package
// fonts (which finds and downloads fonts) aliases them.
package fontdata

// Variant is one weight/style cut of a Font.
type Variant struct {
	Weight int    `json:"weight"` // CSS weight, 100-900
	Italic bool   `json:"italic"`
	Style  string `json:"style,omitempty"` // the cut's own name, e.g. "Bold Italic"
	// URL is where the font file can be fetched, for repositories that
	// serve files; empty for fonts already installed on the machine,
	// which CSS can use by name alone.
	URL string `json:"url,omitempty"`
	// Key is where a Library keeps its downloaded copy of the file in its
	// store; empty until the font has been used.
	Key string `json:"key,omitempty"`
}

// Font is a family and the cuts a repository has of it.
type Font struct {
	Family   string    `json:"family"`
	Source   string    `json:"source"`             // the repository's name
	Category string    `json:"category,omitempty"` // serif, sans-serif, monospace, display, handwriting
	Variants []Variant `json:"variants"`
}

// Closest returns the variant of f nearest the requested weight and
// italic-ness (italic matters most), or false if f has none.
func (f Font) Closest(weight int, italic bool) (Variant, bool) {
	best, bestScore, found := Variant{}, 1<<30, false
	for _, v := range f.Variants {
		score := abs(v.Weight - weight)
		if v.Italic != italic {
			score += 10000
		}
		if score < bestScore {
			best, bestScore, found = v, score, true
		}
	}
	return best, found
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
