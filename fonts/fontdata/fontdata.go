package fontdata

// Variant is one weight/style cut of a Font.
type Variant struct {
	Weight int    `json:"weight"` // CSS weight, 100-900
	Italic bool   `json:"italic"`
	Style  string `json:"style,omitempty"` // the cut's own name, e.g. "Bold Italic"
	URL    string `json:"url,omitempty"`
	Key    string `json:"key,omitempty"`
}

type Font struct {
	Family   string    `json:"family"`
	Source   string    `json:"source"`             // the repository's name
	Category string    `json:"category,omitempty"` // serif, sans-serif, monospace, display, handwriting
	Variants []Variant `json:"variants"`
}

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
