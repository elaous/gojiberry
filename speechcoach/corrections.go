package speechcoach

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Corrections is the memory of what a recogniser got wrong for this person: when they fix a recognised
// word, the pair (heard, meant) is kept, counted, and offered next time that word is recognised. It is
// not a model: it changes nothing in the recogniser, it only remembers and suggests, and the person
// decides each time. It also lists the words the person meant, for the autocomplete list (roadmap 79,
// complete.Index.AddVocabulary).
type Corrections struct {
	pairs []pair
	seq   int
}

type pair struct {
	Heard string `json:"heard"` // lower case, one to three words
	Meant string `json:"meant"` // as the person wrote it
	Count int    `json:"count"`
	Seq   int    `json:"seq"` // when last seen (a counter, not a clock)
}

const (
	maxPairs   = 2000
	maxPairLen = 3 // words on a side
)

// words splits text into words (letters, digits, and apostrophes or hyphens inside a word).
func words(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !isWordRune(r)
	})
}

func lowerAll(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = strings.ToLower(w)
	}
	return out
}

// Learn compares what was recognised with what the person made of it and remembers each difference. It
// aligns the two word lists (longest common run), and for each stretch that differs it keeps the pair:
// word for word when the stretches have the same length, else the whole stretch (up to three words a
// side). It returns the number of pairs noted. Identical texts, and stretches that are only added or
// removed words, note nothing: that is an edit, not a correction of a heard word.
func (c *Corrections) Learn(recognised, corrected string) int {
	a, b := words(recognised), words(corrected)
	la, lb := lowerAll(a), lowerAll(b)
	// longest common subsequence table
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if la[i] == lb[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	noted := 0
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && la[i] == lb[j] {
			i++
			j++
			continue
		}
		si, sj := i, j
		for (i < len(a) || j < len(b)) && (i >= len(a) || j >= len(b) || la[i] != lb[j]) {
			switch {
			case i < len(a) && (j >= len(b) || t[i+1][j] >= t[i][j+1]):
				i++
			default:
				j++
			}
		}
		h, m := a[si:i], b[sj:j]
		switch {
		case len(h) == 0 || len(m) == 0:
		case len(h) == len(m):
			for k := range h {
				if !strings.EqualFold(h[k], m[k]) {
					c.note(h[k], m[k])
					noted++
				}
			}
		case len(h) <= maxPairLen && len(m) <= maxPairLen:
			c.note(strings.Join(h, " "), strings.Join(m, " "))
			noted++
		}
	}
	return noted
}

// note counts one pair.
func (c *Corrections) note(heard, meant string) {
	heard = strings.ToLower(strings.TrimSpace(heard))
	meant = strings.TrimSpace(meant)
	if heard == "" || meant == "" || utf8.RuneCountInString(heard) > 80 || utf8.RuneCountInString(meant) > 80 || strings.ToLower(meant) == heard {
		return
	}
	c.seq++
	for k := range c.pairs {
		if c.pairs[k].Heard == heard && c.pairs[k].Meant == meant {
			c.pairs[k].Count++
			c.pairs[k].Seq = c.seq
			return
		}
	}
	if len(c.pairs) >= maxPairs { // forget the least used, oldest first
		worst := 0
		for k := range c.pairs {
			if c.pairs[k].Count < c.pairs[worst].Count || c.pairs[k].Count == c.pairs[worst].Count && c.pairs[k].Seq < c.pairs[worst].Seq {
				worst = k
			}
		}
		c.pairs = append(c.pairs[:worst], c.pairs[worst+1:]...)
	}
	c.pairs = append(c.pairs, pair{Heard: heard, Meant: meant, Count: 1, Seq: c.seq})
}

// Len is the number of pairs remembered.
func (c *Corrections) Len() int { return len(c.pairs) }

// Suggestion is an offer to change a stretch of recognised text.
type Suggestion struct {
	Heard string // the words as recognised
	Meant string // what they were corrected to before
	Count int    // how often
	Start int    // rune offset of the stretch in the text
	End   int
}

// Suggest returns the earlier corrections that apply to text, in the order of the text. Where several
// meanings are remembered for the same stretch, the most often chosen (then the latest) comes first. The
// text is never changed: the person accepts or ignores each offer.
func (c *Corrections) Suggest(text string) []Suggestion {
	if len(c.pairs) == 0 {
		return nil
	}
	rs := []rune(text)
	type tok struct{ s, e int }
	var toks []tok
	for i := 0; i < len(rs); {
		if !isWordRune(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		toks = append(toks, tok{i, j})
		i = j
	}
	var out []Suggestion
	for k := 0; k < len(toks); k++ {
		for n := maxPairLen; n >= 1; n-- {
			if k+n > len(toks) {
				continue
			}
			// the words must be next to each other, separated by spaces only
			ok := true
			for q := k; q < k+n-1; q++ {
				if strings.TrimSpace(string(rs[toks[q].e:toks[q+1].s])) != "" {
					ok = false
				}
			}
			if !ok {
				continue
			}
			s := strings.ToLower(strings.Join(strings.Fields(string(rs[toks[k].s:toks[k+n-1].e])), " "))
			var hits []pair
			for _, p := range c.pairs {
				if p.Heard == s {
					hits = append(hits, p)
				}
			}
			if len(hits) == 0 {
				continue
			}
			sort.Slice(hits, func(x, y int) bool {
				if hits[x].Count != hits[y].Count {
					return hits[x].Count > hits[y].Count
				}
				return hits[x].Seq > hits[y].Seq
			})
			for _, p := range hits {
				out = append(out, Suggestion{Heard: string(rs[toks[k].s:toks[k+n-1].e]), Meant: p.Meant, Count: p.Count, Start: toks[k].s, End: toks[k+n-1].e})
			}
			k += n - 1
			break
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-'
}

// Words lists the words the person meant, most chosen first, without duplicates: a vocabulary for the
// autocomplete list and for the recogniser's prompt.
func (c *Corrections) Words() []string {
	ps := append([]pair(nil), c.pairs...)
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Count != ps[j].Count {
			return ps[i].Count > ps[j].Count
		}
		return ps[i].Seq > ps[j].Seq
	})
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		for _, w := range words(p.Meant) {
			if k := strings.ToLower(w); !seen[k] {
				seen[k] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// Forget removes the pair (heard, meant); it reports whether there was one.
func (c *Corrections) Forget(heard, meant string) bool {
	heard = strings.ToLower(strings.TrimSpace(heard))
	for k, p := range c.pairs {
		if p.Heard == heard && p.Meant == meant {
			c.pairs = append(c.pairs[:k], c.pairs[k+1:]...)
			return true
		}
	}
	return false
}

// Clear forgets everything.
func (c *Corrections) Clear() { c.pairs, c.seq = nil, 0 }

type correctionsJSON struct {
	Version int    `json:"version"`
	Seq     int    `json:"seq"`
	Pairs   []pair `json:"pairs"`
}

// Marshal saves the memory.
func (c *Corrections) Marshal() ([]byte, error) {
	return json.Marshal(correctionsJSON{Version: 1, Seq: c.seq, Pairs: c.pairs})
}

// UnmarshalCorrections reads what Marshal wrote, dropping pairs that do not fit the rules (a damaged or
// edited file cannot add anything Learn would not).
func UnmarshalCorrections(b []byte) (*Corrections, error) {
	var j correctionsJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	c := &Corrections{seq: j.Seq}
	for _, p := range j.Pairs {
		if len(c.pairs) >= maxPairs {
			break
		}
		if p.Heard == "" || p.Meant == "" || p.Count < 1 || utf8.RuneCountInString(p.Heard) > 80 || utf8.RuneCountInString(p.Meant) > 80 || p.Heard != strings.ToLower(p.Heard) {
			continue
		}
		if p.Seq > c.seq {
			c.seq = p.Seq
		}
		c.pairs = append(c.pairs, p)
	}
	return c, nil
}
