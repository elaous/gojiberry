// Package reference formats citations and bibliographies in the styles of publications: APA, MLA, Chicago
// (author-date), IEEE, Harvard, Vancouver and ABNT. A Source is what the writer typed once (an author, a title, a
// year); the style decides how it is written in the text and in the list at the end. The output is plain runs, with the
// parts a style sets in italics marked, so any editor can write it with its own formatting.
//
// The styles are the common forms for the four kinds of source below (book, article, web page, report), not the whole
// of any style guide: a source with a part missing is written without it, and nothing is made up.
package reference

import (
	"sort"
	"strings"
	"unicode"
)

// Kind of source.
type Kind string

const (
	Book    Kind = "book"
	Article Kind = "article"
	Web     Kind = "web"
	Report  Kind = "report"
)

// Source is one work that can be cited.
type Source struct {
	ID        string   `json:"id"`
	Kind      Kind     `json:"kind"`
	Authors   []string `json:"authors,omitempty"` // "Last, First Middle"; a name without a comma (an organisation) is kept whole
	Title     string   `json:"title"`
	Container string   `json:"container,omitempty"` // the journal, or the web site
	Publisher string   `json:"publisher,omitempty"`
	Place     string   `json:"place,omitempty"`
	Year      string   `json:"year,omitempty"`
	Edition   string   `json:"edition,omitempty"`
	Volume    string   `json:"volume,omitempty"`
	Issue     string   `json:"issue,omitempty"`
	Pages     string   `json:"pages,omitempty"`
	URL       string   `json:"url,omitempty"`
	DOI       string   `json:"doi,omitempty"`
	Accessed  string   `json:"accessed,omitempty"` // YYYY-MM-DD
}

// Style names a publication style.
type Style string

const (
	APA       Style = "apa"
	MLA       Style = "mla"
	Chicago   Style = "chicago"
	IEEE      Style = "ieee"
	Harvard   Style = "harvard"
	Vancouver Style = "vancouver"
	ABNT      Style = "abnt"
)

// Styles lists the styles with the name a person reads.
var Styles = []struct {
	Style Style
	Name  string
}{
	{APA, "APA 7"}, {MLA, "MLA 9"}, {Chicago, "Chicago (author-date)"}, {IEEE, "IEEE"}, {Harvard, "Harvard"}, {Vancouver, "Vancouver"}, {ABNT, "ABNT"},
}

// Numbered says whether the style cites by number (and lists in order of first citation) rather than by author and year.
func (s Style) Numbered() bool { return s == IEEE || s == Vancouver }

// A Run is a piece of text; Italic marks what the style sets in italics (a title, a journal).
type Run struct {
	Text   string
	Italic bool
}

// Text joins runs.
func Text(rs []Run) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type person struct{ last, first string }

func parse(a string) person {
	last, first, ok := strings.Cut(a, ",")
	if !ok {
		return person{last: strings.TrimSpace(a)}
	}
	return person{strings.TrimSpace(last), strings.TrimSpace(first)}
}

func (p person) initials(dot, space bool) string { // "Ana Maria" -> "A. M." / "AM"
	var parts []string
	for _, w := range strings.Fields(strings.ReplaceAll(p.first, "-", " ")) {
		r := []rune(w)
		if len(r) == 0 {
			continue
		}
		s := string(unicode.ToUpper(r[0]))
		if dot {
			s += "."
		}
		parts = append(parts, s)
	}
	sep := ""
	if space {
		sep = " "
	}
	return strings.Join(parts, sep)
}

func persons(s Source) []person {
	out := make([]person, 0, len(s.Authors))
	for _, a := range s.Authors {
		if strings.TrimSpace(a) != "" {
			out = append(out, parse(a))
		}
	}
	return out
}

// lastNames is the in-text author part: "Silva", "Silva and Costa" (joiner), "Silva et al.".
func lastNames(ps []person, joiner string, etAlFrom int) string {
	switch {
	case len(ps) == 0:
		return ""
	case len(ps) == 1:
		return ps[0].last
	case len(ps) >= etAlFrom:
		return ps[0].last + " et al."
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.last
	}
	if len(names) == 2 {
		return names[0] + " " + joiner + " " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", " + joiner + " " + names[len(names)-1]
}

// Citation is the mark in the text for the source: "(Silva, 2020)" or "[3]". n is its number in the list for the
// numbered styles; page, when given, is added where the style writes it.
func Citation(st Style, s Source, n int, page string) string {
	ps := persons(s)
	who := ""
	year := s.Year
	if year == "" {
		year = "n.d."
	}
	pg := strings.TrimSpace(page)
	switch st {
	case IEEE:
		if pg != "" {
			return "[" + itoa(n) + ", p. " + pg + "]"
		}
		return "[" + itoa(n) + "]"
	case Vancouver:
		if pg != "" {
			return "(" + itoa(n) + ", p. " + pg + ")"
		}
		return "(" + itoa(n) + ")"
	case MLA:
		who = lastNames(ps, "and", 3)
		if who == "" {
			who = "“" + short(s.Title) + "”"
		}
		if pg != "" {
			return "(" + who + " " + pg + ")"
		}
		return "(" + who + ")"
	case Chicago:
		who = lastNames(ps, "and", 4)
		if who == "" {
			who = short(s.Title)
		}
		if pg != "" {
			return "(" + who + " " + year + ", " + pg + ")"
		}
		return "(" + who + " " + year + ")"
	case Harvard:
		who = lastNames(ps, "and", 4)
		if who == "" {
			who = short(s.Title)
		}
		if pg != "" {
			return "(" + who + ", " + year + ", p. " + pg + ")"
		}
		return "(" + who + ", " + year + ")"
	case ABNT:
		who = strings.ToUpper(lastNames(ps, ";", 4))
		if len(ps) > 1 && len(ps) < 4 {
			names := make([]string, len(ps))
			for i, p := range ps {
				names[i] = strings.ToUpper(p.last)
			}
			who = strings.Join(names, "; ")
		}
		if who == "" {
			who = strings.ToUpper(short(s.Title))
		}
		if pg != "" {
			return "(" + who + ", " + year + ", p. " + pg + ")"
		}
		return "(" + who + ", " + year + ")"
	}
	who = lastNames(ps, "&", 3) // APA
	if who == "" {
		who = "“" + short(s.Title) + "”"
	}
	if pg != "" {
		return "(" + who + ", " + year + ", p. " + pg + ")"
	}
	return "(" + who + ", " + year + ")"
}

func itoa(n int) string {
	if n <= 0 {
		return "?"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

func short(title string) string {
	w := strings.Fields(title)
	if len(w) > 4 {
		w = w[:4]
	}
	return strings.Join(w, " ")
}

// Bibliography orders the sources the way the style lists them and writes each one. Author-date styles are in
// alphabetical order of the first author (or the title); numbered styles keep the order given, which is the order of
// first citation. Number is the 1-based position in the returned list.
func Bibliography(st Style, sources []Source) [][]Run {
	list := append([]Source(nil), sources...)
	if !st.Numbered() {
		sort.SliceStable(list, func(i, j int) bool { return sortKey(list[i]) < sortKey(list[j]) })
	}
	out := make([][]Run, len(list))
	for i, s := range list {
		out[i] = Entry(st, s, i+1)
	}
	return out
}

func sortKey(s Source) string {
	if ps := persons(s); len(ps) > 0 {
		return strings.ToLower(ps[0].last + " " + ps[0].first + " " + s.Year)
	}
	return strings.ToLower(s.Title)
}

// Order gives the position (1-based) each source has in Bibliography's list, by ID.
func Order(st Style, sources []Source) map[string]int {
	list := append([]Source(nil), sources...)
	if !st.Numbered() {
		sort.SliceStable(list, func(i, j int) bool { return sortKey(list[i]) < sortKey(list[j]) })
	}
	m := map[string]int{}
	for i, s := range list {
		m[s.ID] = i + 1
	}
	return m
}

// builder collects runs and drops the separators of parts that are missing.
type builder struct{ runs []Run }

func (b *builder) add(text string)  { b.runs = append(b.runs, Run{Text: text}) }
func (b *builder) ital(text string) { b.runs = append(b.runs, Run{Text: text, Italic: true}) }
func (b *builder) last() string     { return Text(b.runs) }

// stop makes sure the entry so far ends with a full stop (or ?, !) before the next part.
func (b *builder) stop() {
	t := strings.TrimRight(b.last(), " ")
	if t == "" || strings.HasSuffix(t, ".") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "!") || strings.HasSuffix(t, ":") {
		return
	}
	b.add(".")
}

func link(s Source) string {
	if s.DOI != "" {
		if strings.HasPrefix(s.DOI, "http") {
			return s.DOI
		}
		return "https://doi.org/" + s.DOI
	}
	return s.URL
}

func dash(p string) string { return strings.ReplaceAll(p, "-", "–") }

// Entry writes one source for the list at the end; n is its number for the numbered styles.
func Entry(st Style, s Source, n int) []Run {
	ps := persons(s)
	b := &builder{}
	switch st {
	case MLA:
		entryMLA(b, s, ps)
	case Chicago:
		entryChicago(b, s, ps)
	case IEEE:
		b.add("[" + itoa(n) + "] ")
		entryIEEE(b, s, ps)
	case Harvard:
		entryHarvard(b, s, ps)
	case Vancouver:
		b.add(itoa(n) + ". ")
		entryVancouver(b, s, ps)
	case ABNT:
		entryABNT(b, s, ps)
	default:
		entryAPA(b, s, ps)
	}
	return b.runs
}

func authorsAPA(ps []person) string {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = p.last
		if in := p.initials(true, true); in != "" {
			n[i] += ", " + in
		}
	}
	switch len(n) {
	case 0:
		return ""
	case 1:
		return n[0]
	case 2:
		return n[0] + ", & " + n[1]
	}
	return strings.Join(n[:len(n)-1], ", ") + ", & " + n[len(n)-1]
}

func entryAPA(b *builder, s Source, ps []person) {
	year := s.Year
	if year == "" {
		year = "n.d."
	}
	if a := authorsAPA(ps); a != "" {
		b.add(a)
		b.stop()
		b.add(" (" + year + "). ")
	}
	switch s.Kind {
	case Article:
		if len(ps) == 0 {
			b.add(s.Title)
			b.stop()
			b.add(" (" + year + "). ")
		} else {
			b.add(s.Title)
			b.stop()
			b.add(" ")
		}
		b.ital(s.Container)
		if s.Volume != "" {
			b.add(", ")
			b.ital(s.Volume)
			if s.Issue != "" {
				b.add("(" + s.Issue + ")")
			}
		}
		if s.Pages != "" {
			b.add(", " + dash(s.Pages))
		}
		b.add(".")
	default:
		if len(ps) == 0 {
			b.ital(s.Title)
			b.stop()
			b.add(" (" + year + "). ")
		} else {
			b.ital(s.Title)
		}
		if s.Edition != "" {
			b.add(" (" + s.Edition + " ed.)")
		}
		b.stop()
		if s.Kind == Web && s.Container != "" && s.Container != s.Publisher {
			b.add(" " + s.Container + ".")
		}
		if s.Publisher != "" {
			b.add(" " + s.Publisher + ".")
		}
	}
	if l := link(s); l != "" {
		b.add(" " + l)
	}
}

func entryMLA(b *builder, s Source, ps []person) {
	switch len(ps) {
	case 0:
	case 1:
		b.add(join(ps[0].last, ps[0].first, ", "))
	case 2:
		b.add(join(ps[0].last, ps[0].first, ", ") + ", and " + strings.TrimSpace(ps[1].first+" "+ps[1].last))
	default:
		b.add(join(ps[0].last, ps[0].first, ", ") + ", et al")
	}
	if len(ps) > 0 {
		b.stop()
		b.add(" ")
	}
	switch s.Kind {
	case Article:
		b.add("“" + strings.TrimRight(s.Title, ".") + ".” ")
		b.ital(s.Container)
		var parts []string
		if s.Volume != "" {
			parts = append(parts, "vol. "+s.Volume)
		}
		if s.Issue != "" {
			parts = append(parts, "no. "+s.Issue)
		}
		if s.Year != "" {
			parts = append(parts, s.Year)
		}
		if s.Pages != "" {
			parts = append(parts, "pp. "+dash(s.Pages))
		}
		if len(parts) > 0 {
			b.add(", " + strings.Join(parts, ", "))
		}
		b.add(".")
	case Web:
		b.add("“" + strings.TrimRight(s.Title, ".") + ".” ")
		if s.Container != "" {
			b.ital(s.Container)
			b.add(", ")
		}
		if s.Publisher != "" {
			b.add(s.Publisher + ", ")
		}
		b.add(strings.TrimSuffix(strings.TrimSpace(s.Year), ","))
		b.stop()
	default:
		b.ital(s.Title)
		b.stop()
		var parts []string
		if s.Edition != "" {
			parts = append(parts, s.Edition+" ed.")
		}
		if s.Publisher != "" {
			parts = append(parts, s.Publisher)
		}
		if s.Year != "" {
			parts = append(parts, s.Year)
		}
		if len(parts) > 0 {
			b.add(" " + strings.Join(parts, ", ") + ".")
		}
	}
	if l := link(s); l != "" {
		b.add(" " + strings.TrimPrefix(strings.TrimPrefix(l, "https://"), "http://") + ".")
	}
}

func join(a, c, sep string) string {
	if c == "" {
		return a
	}
	return a + sep + c
}

func entryChicago(b *builder, s Source, ps []person) {
	for i, p := range ps {
		switch {
		case i == 0:
			b.add(join(p.last, p.first, ", "))
		case i == len(ps)-1:
			b.add(", and " + strings.TrimSpace(p.first+" "+p.last))
		default:
			b.add(", " + strings.TrimSpace(p.first+" "+p.last))
		}
	}
	if len(ps) > 0 {
		b.stop()
		b.add(" ")
	}
	year := s.Year
	if year == "" {
		year = "n.d."
	}
	b.add(year + ". ")
	switch s.Kind {
	case Article:
		b.add("“" + strings.TrimRight(s.Title, ".") + ".” ")
		b.ital(s.Container)
		if s.Volume != "" {
			b.add(" " + s.Volume)
		}
		if s.Issue != "" {
			b.add(" (" + s.Issue + ")")
		}
		if s.Pages != "" {
			b.add(": " + dash(s.Pages))
		}
		b.add(".")
	default:
		b.ital(s.Title)
		b.stop()
		if s.Kind == Web && s.Container != "" {
			b.add(" " + s.Container + ".")
		}
		pub := s.Publisher
		if s.Place != "" && pub != "" {
			pub = s.Place + ": " + pub
		}
		if pub != "" {
			b.add(" " + pub + ".")
		}
	}
	if l := link(s); l != "" {
		b.add(" " + l + ".")
	}
}

func authorsIEEE(ps []person) string {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = strings.TrimSpace(p.initials(true, true) + " " + p.last)
	}
	switch len(n) {
	case 0:
		return ""
	case 1:
		return n[0]
	case 2:
		return n[0] + " and " + n[1]
	}
	if len(n) > 6 {
		return n[0] + " et al."
	}
	return strings.Join(n[:len(n)-1], ", ") + ", and " + n[len(n)-1]
}

func entryIEEE(b *builder, s Source, ps []person) {
	a := authorsIEEE(ps)
	if a != "" {
		b.add(a + ", ")
	}
	switch s.Kind {
	case Article:
		b.add("“" + strings.TrimRight(s.Title, ".") + ",” ")
		b.ital(s.Container)
		if s.Volume != "" {
			b.add(", vol. " + s.Volume)
		}
		if s.Issue != "" {
			b.add(", no. " + s.Issue)
		}
		if s.Pages != "" {
			b.add(", pp. " + dash(s.Pages))
		}
		if s.Year != "" {
			b.add(", " + s.Year)
		}
		b.add(".")
	default:
		b.ital(s.Title)
		if s.Edition != "" {
			b.add(", " + s.Edition + " ed")
		}
		b.add(". ")
		pub := s.Publisher
		if s.Place != "" && pub != "" {
			pub = s.Place + ": " + pub
		}
		if pub != "" {
			b.add(pub)
		}
		if s.Year != "" {
			if pub != "" {
				b.add(", ")
			}
			b.add(s.Year)
		}
		b.add(".")
	}
	if l := link(s); l != "" {
		b.add(" [Online]. Available: " + l)
	}
}

func entryHarvard(b *builder, s Source, ps []person) {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = p.last
		if in := p.initials(true, false); in != "" {
			n[i] += ", " + in
		}
	}
	a := ""
	switch len(n) {
	case 0:
	case 1:
		a = n[0]
	default:
		a = strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
	}
	year := s.Year
	if year == "" {
		year = "n.d."
	}
	if a != "" {
		b.add(a + " (" + year + ") ")
	}
	switch s.Kind {
	case Article:
		b.add("‘" + strings.TrimRight(s.Title, ".") + "’, ")
		b.ital(s.Container)
		if s.Volume != "" {
			b.add(", " + s.Volume)
			if s.Issue != "" {
				b.add("(" + s.Issue + ")")
			}
		}
		if s.Pages != "" {
			b.add(", pp. " + dash(s.Pages))
		}
		b.add(".")
	default:
		if a == "" {
			b.ital(s.Title)
			b.add(" (" + year + ")")
		} else {
			b.ital(s.Title)
		}
		b.stop()
		if s.Edition != "" {
			b.add(" " + s.Edition + " edn.")
		}
		pub := s.Publisher
		if s.Place != "" && pub != "" {
			pub = s.Place + ": " + pub
		}
		if pub != "" {
			b.add(" " + pub + ".")
		}
	}
	if l := link(s); l != "" {
		b.add(" Available at: " + l)
		if s.Accessed != "" {
			b.add(" (Accessed: " + s.Accessed + ")")
		}
		b.add(".")
	}
}

func entryVancouver(b *builder, s Source, ps []person) {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = strings.TrimSpace(p.last + " " + p.initials(false, false))
	}
	if len(n) > 6 {
		n = append(n[:6], "et al")
	}
	if len(n) > 0 {
		b.add(strings.Join(n, ", "))
		b.stop()
		b.add(" ")
	}
	switch s.Kind {
	case Article:
		b.add(strings.TrimRight(s.Title, ".") + ". ")
		b.add(s.Container + ". " + s.Year)
		if s.Volume != "" {
			b.add(";" + s.Volume)
			if s.Issue != "" {
				b.add("(" + s.Issue + ")")
			}
		}
		if s.Pages != "" {
			b.add(":" + strings.ReplaceAll(s.Pages, "–", "-"))
		}
		b.add(".")
	default:
		b.add(strings.TrimRight(s.Title, ".") + ". ")
		if s.Edition != "" {
			b.add(s.Edition + " ed. ")
		}
		pub := s.Publisher
		if s.Place != "" && pub != "" {
			pub = s.Place + ": " + pub
		}
		if pub != "" {
			b.add(pub)
		}
		if s.Year != "" {
			if pub != "" {
				b.add("; ")
			}
			b.add(s.Year)
		}
		b.add(".")
	}
	if l := link(s); l != "" {
		b.add(" Available from: " + l)
	}
}

func entryABNT(b *builder, s Source, ps []person) {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = strings.ToUpper(p.last)
		if p.first != "" {
			n[i] += ", " + p.first
		}
	}
	if len(n) > 3 {
		n = []string{n[0] + " et al."}
	}
	if len(n) > 0 {
		b.add(strings.Join(n, "; "))
		b.stop()
		b.add(" ")
	}
	switch s.Kind {
	case Article:
		b.add(strings.TrimRight(s.Title, ".") + ". ")
		b.ital(s.Container)
		if s.Place != "" {
			b.add(", " + s.Place)
		}
		if s.Volume != "" {
			b.add(", v. " + s.Volume)
		}
		if s.Issue != "" {
			b.add(", n. " + s.Issue)
		}
		if s.Pages != "" {
			b.add(", p. " + strings.ReplaceAll(s.Pages, "–", "-"))
		}
		if s.Year != "" {
			b.add(", " + s.Year)
		}
		b.add(".")
	default:
		b.ital(s.Title)
		b.stop()
		if s.Edition != "" {
			b.add(" " + s.Edition + " ed.")
		}
		var parts []string
		if s.Place != "" {
			parts = append(parts, s.Place)
		}
		if s.Publisher != "" {
			if len(parts) > 0 {
				parts[len(parts)-1] += ":"
			}
			parts = append(parts, s.Publisher+",")
		}
		pub := strings.Join(parts, " ")
		if s.Year != "" {
			if pub != "" {
				pub += " "
			}
			pub += s.Year + "."
		}
		if pub != "" {
			b.add(" " + strings.TrimSuffix(pub, ",") + func() string {
				if strings.HasSuffix(pub, ".") || strings.HasSuffix(pub, ",") {
					return ""
				}
				return "."
			}())
		}
	}
	if l := link(s); l != "" {
		b.add(" Disponível em: " + l + ".")
		if s.Accessed != "" {
			b.add(" Acesso em: " + s.Accessed + ".")
		}
	}
}
