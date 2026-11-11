package canvas

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Markdown is a Transform for extended Markdown syntax.
type Markdown struct {
	CodeFamily     string // default "monospace"
	HeadingScale   [6]float64
	LinkColor      string // default LinkColor ("#1a5fb4")
	CodeBackground string // default "#eceef1"
	MarkBackground string // ==highlight==, default "#fff3a3"
	QuoteColor     string // default "#5b616b"
	QuoteBar       string // the bar beside a block quote, default "#c4c7cd"
	Images         func(url, alt string) InlineObject
}

func (m Markdown) codeFamily() string { return orStr(m.CodeFamily, "monospace") }

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (m Markdown) headingScale(level int) float64 {
	if v := m.HeadingScale[level-1]; v > 0 {
		return v
	}
	return [6]float64{2, 1.6, 1.35, 1.2, 1.05, 1}[level-1]
}

var (
	reHeading   = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)(\s+#+)?\s*$`)
	reRule      = regexp.MustCompile(`^ {0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	reFence     = regexp.MustCompile("^ {0,3}(```|~~~)")
	reListItem  = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	reTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	reColumns   = regexp.MustCompile(`^\\columns\s+(\d+)(?:\s+gap=(\d+(?:\.\d+)?))?\s*$`)
	reTask      = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	reListStart = regexp.MustCompile(`^(\d+)`)
)

// Transform implements Transform.
func (m Markdown) Transform(src string) []Item {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(src, "\n")
	var out []Item
	text := func(s string, f Format) { out = append(out, Chars{Text: s, Format: f}) }

	isBlockStart := func(i int) bool {
		l := lines[i]
		return strings.TrimSpace(l) == "" || reHeading.MatchString(l) || reRule.MatchString(l) ||
			reFence.MatchString(l) || reListItem.MatchString(l) || strings.HasPrefix(strings.TrimSpace(l), ">") ||
			strings.HasPrefix(l, `\`) && isDirective(l) || (strings.Contains(l, "|") && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]))
	}

	gap := func(i int) {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i < len(lines) && !isDirective(lines[i]) {
			text("\n", Format{})
		}
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++

		case isDirective(line):
			i++
			d := strings.TrimSpace(line)
			switch d {
			case `\pagebreak`:
				out = append(out, Break{Kind: BreakPage})
			case `\columnbreak`:
				out = append(out, Break{Kind: BreakColumn})
			default:
				mm := reColumns.FindStringSubmatch(d)
				n, _ := strconv.Atoi(mm[1])
				gap := 24.0
				if mm[2] != "" {
					gap, _ = strconv.ParseFloat(mm[2], 64)
				}
				if n <= 1 {
					out = append(out, ColumnConfig{})
				} else {
					out = append(out, ColumnConfig{Count: n, Gap: gap})
				}
			}

		case reFence.MatchString(line):
			fence := reFence.FindStringSubmatch(line)[1]
			i++
			code := Format{FontFamily: m.codeFamily(), Background: orStr(m.CodeBackground, "#eceef1")}
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				text(lines[i]+"\n", code)
				i++
			}
			i++ // closing fence
			gap(i)

		case reHeading.MatchString(line):
			mm := reHeading.FindStringSubmatch(line)
			level := len(mm[1])
			base := Format{Scale: m.headingScale(level), FontWeight: 700}
			out = append(out, m.inline(mm[2], base)...)
			text("\n", Format{})
			i++

		case reRule.MatchString(line):
			out = append(out, Inline{Object: &Rule{}, Block: true})
			text("\n", Format{})
			i++

		case strings.Contains(line, "|") && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]):
			var rows [][]string
			rows = append(rows, m.cells(line))
			i += 2
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "|") {
				rows = append(rows, m.cells(lines[i]))
				i++
			}
			out = append(out, Inline{Object: NewTableObject(NewTableFrom(rows)), Block: true})
			text("\n", Format{})

		case strings.HasPrefix(strings.TrimSpace(line), ">"):
			quote := Format{Color: orStr(m.QuoteColor, "#5b616b"), FontStyle: StyleItalic}
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				body := strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")
				body = strings.TrimPrefix(body, " ")
				out = append(out, ParaFormat{QuoteBar: orStr(m.QuoteBar, "#c4c7cd"), IndentLeft: 16})
				out = append(out, m.inline(body, quote)...)
				text("\n", Format{})
				i++
			}
			gap(i)

		case reListItem.MatchString(line):
			started := map[int]bool{} // levels whose numbering has begun in this list
			for i < len(lines) {
				mm := reListItem.FindStringSubmatch(lines[i])
				if mm == nil {
					break
				}
				level := len(strings.ReplaceAll(mm[1], "\t", "  ")) / 2
				for l := range started {
					if l > level {
						delete(started, l)
					}
				}
				marker, body := mm[2], mm[3]
				ls := ListStyle{Kind: ListBullet, Level: level}
				if reListStart.MatchString(marker) {
					ls.Kind = ListOrdered
					if !started[level] {
						started[level] = true
						if n, _ := strconv.Atoi(reListStart.FindString(marker)); n != 1 {
							ls.Start = n
						}
					}
					if strings.HasSuffix(marker, ")") {
						ls.Marker = "{n})"
					}
				}
				if tm := reTask.FindStringSubmatch(body); tm != nil {
					ls.Kind, ls.Checked, body = ListCheck, tm[1] != " ", tm[2]
				}
				i++
				for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !reListItem.MatchString(lines[i]) && strings.HasPrefix(lines[i], " ") {
					body += " " + strings.TrimSpace(lines[i]) // a continuation line
					i++
				}
				out = append(out, ParaFormat{List: ls})
				out = append(out, m.inline(body, Format{})...)
				text("\n", Format{})
			}
			gap(i)

		default: // a paragraph
			var para strings.Builder
			for i < len(lines) && (para.Len() == 0 || !isBlockStart(i)) {
				l := lines[i]
				hard := strings.HasSuffix(l, "  ") || strings.HasSuffix(l, `\`)
				l = strings.TrimSuffix(strings.TrimRight(l, " "), `\`)
				if para.Len() > 0 && !strings.HasSuffix(para.String(), "\n") {
					para.WriteString(" ")
				}
				para.WriteString(strings.TrimLeft(l, " "))
				if hard {
					para.WriteString("\n")
				}
				i++
			}
			out = append(out, m.inline(strings.TrimRight(para.String(), " "), Format{})...)
			text("\n", Format{})
			gap(i)
		}
	}
	return trimTrailingNewlines(out)
}

func isDirective(line string) bool {
	d := strings.TrimSpace(line)
	return d == `\pagebreak` || d == `\columnbreak` || reColumns.MatchString(d)
}

func trimTrailingNewlines(items []Item) []Item {
	for len(items) > 0 {
		c, ok := items[len(items)-1].(Chars)
		if !ok {
			break
		}
		c.Text = strings.TrimRight(c.Text, "\n")
		if c.Text != "" {
			items[len(items)-1] = c
			break
		}
		items = items[:len(items)-1]
	}
	return items
}

func (m Markdown) cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = strings.TrimSuffix(row, "|")
	}
	var cells []string
	var cur strings.Builder
	r := []rune(row)
	for i := 0; i < len(r); i++ {
		switch {
		case r[i] == '\\' && i+1 < len(r) && r[i+1] == '|':
			cur.WriteRune('|')
			i++
		case r[i] == '|':
			cells = append(cells, m.plain(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r[i])
		}
	}
	return append(cells, m.plain(cur.String()))
}

func (m Markdown) plain(s string) string {
	var b strings.Builder
	for _, it := range m.inline(strings.TrimSpace(s), Format{}) {
		if c, ok := it.(Chars); ok {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

type mdState struct{ bold, italic, strike, under, sup, sub, mark bool }

func (st mdState) format(base Format, m Markdown) Format {
	f := base
	if st.bold {
		f.FontWeight = 700
	}
	if st.italic {
		f.FontStyle = StyleItalic
	}
	f.Strike = f.Strike || st.strike
	f.Underline = f.Underline || st.under
	f.Superscript = f.Superscript || st.sup
	f.Subscript = f.Subscript || st.sub
	if st.mark {
		f.Background = orStr(m.MarkBackground, "#fff3a3")
	}
	return f
}

var mdDelims = []string{"***", "**", "__", "~~", "++", "==", "*", "_", "~", "^"}

func (st *mdState) flag(tok string) []*bool {
	switch tok {
	case "***":
		return []*bool{&st.bold, &st.italic}
	case "**", "__":
		return []*bool{&st.bold}
	case "*", "_":
		return []*bool{&st.italic}
	case "~~":
		return []*bool{&st.strike}
	case "++":
		return []*bool{&st.under}
	case "==":
		return []*bool{&st.mark}
	case "~":
		return []*bool{&st.sub}
	case "^":
		return []*bool{&st.sup}
	}
	return nil
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func runLen(r []rune, i int, c rune) int {
	n := 0
	for i+n < len(r) && r[i+n] == c {
		n++
	}
	return n
}

func findCloser(r []rune, from int, tok string) int {
	c := []rune(tok)[0]
	for j := from; j < len(r); j++ {
		if r[j] == '\\' {
			j++
			continue
		}
		if r[j] != c {
			continue
		}
		n := runLen(r, j, c)
		if n == len(tok) && !unicode.IsSpace(r[j-1]) && (c != '_' || j+n >= len(r) || !isWordRune(r[j+n])) {
			if (tok == "^" || tok == "~") && containsSpace(r[from:j]) {
				return -1
			}
			return j
		}
		j += n - 1
	}
	return -1
}

func containsSpace(r []rune) bool {
	for _, c := range r {
		if unicode.IsSpace(c) {
			return true
		}
	}
	return false
}

func (m Markdown) inline(s string, base Format) []Item {
	var out []Item
	var sb strings.Builder
	var st mdState
	flush := func() {
		if sb.Len() > 0 {
			out = append(out, Chars{Text: sb.String(), Format: st.format(base, m)})
			sb.Reset()
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r) && strings.ContainsRune("\\`*_{}[]()#+-.!|~^=<>", r[i+1]):
			sb.WriteRune(r[i+1])
			i += 2
			continue

		case c == '`':
			n := runLen(r, i, '`')
			if j := findRun(r, i+n, '`', n); j >= 0 {
				flush()
				code := strings.TrimSpace(string(r[i+n : j]))
				f := st.format(base, m)
				f.FontFamily, f.Background = m.codeFamily(), orStr(m.CodeBackground, "#eceef1")
				out = append(out, Chars{Text: code, Format: f})
				i = j + n
				continue
			}

		case c == '!' && i+1 < len(r) && r[i+1] == '[':
			if alt, url, end, ok := parseLink(r, i+1); ok {
				flush()
				var obj InlineObject
				if m.Images != nil {
					obj = m.Images(url, alt)
				}
				if obj != nil {
					out = append(out, Inline{Object: obj})
				} else {
					f := st.format(base, m)
					f.FontStyle = StyleItalic
					out = append(out, Chars{Text: "[" + alt + "]", Format: f})
				}
				i = end
				continue
			}

		case c == '[':
			if label, url, end, ok := parseLink(r, i); ok {
				flush()
				f := st.format(base, m)
				f.Link = url
				if m.LinkColor != "" {
					f.Color = m.LinkColor
				}
				out = append(out, m.inline(label, f)...)
				i = end
				continue
			}

		default:
			if tok, ok := m.delimAt(r, i, &st); ok {
				flush()
				for _, f := range st.flag(tok) {
					*f = !*f
				}
				i += len(tok)
				continue
			}
		}
		sb.WriteRune(c)
		i++
	}
	flush()
	return out
}

func (m Markdown) delimAt(r []rune, i int, st *mdState) (string, bool) {
	for _, tok := range mdDelims {
		n := len(tok)
		if i+n > len(r) || string(r[i:i+n]) != tok {
			continue
		}
		c := r[i]
		if runLen(r, i, c) != n { // part of a longer or shorter run
			continue
		}
		prevSpace := i == 0 || unicode.IsSpace(r[i-1])
		nextSpace := i+n >= len(r) || unicode.IsSpace(r[i+n])
		active := true
		for _, f := range st.flag(tok) {
			active = active && *f
		}
		if active { // a closer
			if prevSpace || (c == '_' && i+n < len(r) && isWordRune(r[i+n])) {
				return "", false
			}
			return tok, true
		}
		if nextSpace || (c == '_' && i > 0 && isWordRune(r[i-1])) {
			return "", false
		}
		if findCloser(r, i+n, tok) < 0 {
			return "", false
		}
		return tok, true
	}
	return "", false
}

func findRun(r []rune, from int, c rune, n int) int {
	for j := from; j < len(r); j++ {
		if r[j] != c {
			continue
		}
		k := runLen(r, j, c)
		if k == n {
			return j
		}
		j += k - 1
	}
	return -1
}

func parseLink(r []rune, i int) (label, url string, end int, ok bool) {
	depth, j := 0, i
	for ; j < len(r); j++ {
		if r[j] == '\\' {
			j++
			continue
		}
		if r[j] == '[' {
			depth++
		} else if r[j] == ']' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if j >= len(r) || j+1 >= len(r) || r[j+1] != '(' {
		return "", "", 0, false
	}
	k := j + 2
	for k < len(r) && r[k] != ')' {
		k++
	}
	if k >= len(r) {
		return "", "", 0, false
	}
	url = strings.TrimSpace(string(r[j+2 : k]))
	if sp := strings.IndexByte(url, ' '); sp >= 0 { // drop a "title"
		url = url[:sp]
	}
	return string(r[i+1 : j]), url, k + 1, true
}

var _ Transform = Markdown{}
