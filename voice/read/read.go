package read

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elaous/gojiberry/components"
)

// Chunk is one piece to speak.
type Chunk struct {
	Text    string
	Lang    string
	Block   int
	Offsets []int
}

func (c Chunk) Span() (from, to int, ok bool) {
	if len(c.Offsets) == 0 {
		return 0, 0, false
	}
	return c.Offsets[0], c.Offsets[len(c.Offsets)-1] + 1, true
}

func (c Chunk) WordAt(i, length int) (from, to int, ok bool) {
	rs := []rune(c.Text)
	if len(c.Offsets) != len(rs) || i < 0 || i >= len(rs) {
		return 0, 0, false
	}
	end := i + length
	if length <= 0 {
		end = i
		for end < len(rs) && rs[end] != ' ' {
			end++
		}
	}
	end = min(max(end, i+1), len(rs))
	return c.Offsets[i], c.Offsets[end-1] + 1, true
}

const MaxChunk = 200

const sentenceMarks = ".!?…。！？"
const closers = `"')]”’»`

type spokenRune struct {
	r   rune
	off int
}

// Chunks is the blocks as pieces to speak, in order.
func Chunks(blocks []components.A11yBlock, docLang string) []Chunk {
	var out []Chunk
	for i, b := range blocks {
		if b.Kind == components.A11yTableRow {
			out = appendSentences(out, plain(strings.Join(nonEmpty(b.Cells), ", ")), docLang, i)
			continue
		}
		runs := b.Runs
		if len(runs) == 0 {
			runs = []components.A11yRun{{Text: b.Text}}
		}
		start := len(out)
		type group struct {
			lang string
			text []spokenRune
		}
		var spoken []group
		pos := 0
		for _, r := range runs {
			n := utf8.RuneCountInString(r.Text)
			width := n
			if r.Stream > 0 {
				width = r.Stream
			}
			if r.Change != "del" {
				lang := r.Lang
				if lang == "" {
					lang = docLang
				}
				if len(spoken) == 0 || spoken[len(spoken)-1].lang != lang {
					spoken = append(spoken, group{lang: lang})
				}
				g := &spoken[len(spoken)-1]
				k := 0
				for _, rn := range r.Text {
					off := -1
					if b.Mapped && !b.Object {
						at := pos + k
						if r.Stream > 0 {
							at = pos
						}
						off = b.Start + max(at-b.Lead, 0)
					}
					g.text = append(g.text, spokenRune{rn, off})
					k++
				}
			}
			pos += width
		}
		for _, g := range spoken {
			out = appendSentences(out, g.text, g.lang, i)
		}
		if len(out) > start && b.Kind != components.A11yParagraph && !endsSentence(out[len(out)-1].Text) {
			last := &out[len(out)-1]
			last.Text += "."
			if len(last.Offsets) > 0 {
				last.Offsets = append(last.Offsets, last.Offsets[len(last.Offsets)-1])
			}
		}
	}
	return out
}

func OfText(text, lang string, from int) []Chunk {
	rs := make([]spokenRune, 0, len(text))
	k := 0
	for _, r := range text {
		off := -1
		if from >= 0 {
			off = from + k
		}
		rs = append(rs, spokenRune{r, off})
		k++
	}
	return appendSentences(nil, rs, lang, -1)
}

func plain(text string) []spokenRune {
	var rs []spokenRune
	for _, r := range text {
		rs = append(rs, spokenRune{r, -1})
	}
	return rs
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func endsSentence(s string) bool {
	rs := []rune(strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(closers, r) }))
	return len(rs) == 0 || strings.ContainsRune(sentenceMarks, rs[len(rs)-1])
}

func normalize(in []spokenRune) []spokenRune {
	var out []spokenRune
	for _, c := range in {
		if unicode.IsSpace(c.r) {
			if len(out) > 0 && out[len(out)-1].r != ' ' {
				out = append(out, spokenRune{' ', c.off})
			}
			continue
		}
		out = append(out, c)
	}
	if n := len(out); n > 0 && out[n-1].r == ' ' {
		out = out[:n-1]
	}
	return out
}

func toChunk(rs []spokenRune, lang string, block int) Chunk {
	var sb strings.Builder
	offs := make([]int, 0, len(rs))
	known := false
	for _, c := range rs {
		sb.WriteRune(c.r)
		offs = append(offs, c.off)
		known = known || c.off >= 0
	}
	if !known {
		offs = nil
	}
	return Chunk{Text: sb.String(), Lang: lang, Block: block, Offsets: offs}
}

func appendSentences(out []Chunk, text []spokenRune, lang string, block int) []Chunk {
	rs := normalize(text)
	start := 0
	flush := func(end int) {
		piece := trimSpaces(rs[start:end])
		start = end
		for _, p := range splitLong(piece, MaxChunk) {
			out = append(out, toChunk(p, lang, block))
		}
	}
	for i, c := range rs {
		if !strings.ContainsRune(sentenceMarks, c.r) {
			continue
		}
		j := i + 1
		for j < len(rs) && strings.ContainsRune(closers, rs[j].r) {
			j++
		}
		if j == len(rs) || rs[j].r == ' ' || strings.ContainsRune("。！？", c.r) {
			flush(j)
		}
	}
	if start < len(rs) {
		flush(len(rs))
	}
	return out
}

func trimSpaces(rs []spokenRune) []spokenRune {
	for len(rs) > 0 && rs[0].r == ' ' {
		rs = rs[1:]
	}
	for len(rs) > 0 && rs[len(rs)-1].r == ' ' {
		rs = rs[:len(rs)-1]
	}
	return rs
}

func splitLong(rs []spokenRune, max int) [][]spokenRune {
	var out [][]spokenRune
	for len(rs) > max {
		cut := max
		for cut > 0 && rs[cut].r != ' ' {
			cut--
		}
		if cut == 0 {
			cut = max
		}
		out = append(out, trimSpaces(rs[:cut]))
		rs = trimSpaces(rs[cut:])
	}
	if len(rs) > 0 {
		out = append(out, rs)
	}
	return out
}
