package canvas

// Table styles: a named look for a whole table (a header row in a colour, bands on every second row, bold first
// column) applied in one step. A style is applied as cell formatting (CellStyle), so it is saved with the table,
// written by every export that writes cell shading and colour, and can still be changed cell by cell afterwards.
// Alignment and italics a cell already has are kept; background, colour and bold are set by the style.

// TableScheme is a named look for a table.
type TableScheme struct {
	Name string
	// HeaderBG and HeaderColor dress the first row; HeaderBold makes it bold.
	HeaderBG, HeaderColor string
	HeaderBold            bool
	// BandBG is the background of every second row of the body (the first body row is plain, the second banded...).
	BandBG string
	// FirstColBold makes the first column of the body bold.
	FirstColBold bool
}

// TableSchemes are the styles the editor offers, the first (Plain) taking every style off.
func TableSchemes() []TableScheme {
	return []TableScheme{
		{Name: "Plain"},
		{Name: "Bold header", HeaderBold: true},
		{Name: "Blue header", HeaderBG: "#2563eb", HeaderColor: "#ffffff", HeaderBold: true},
		{Name: "Blue header, banded", HeaderBG: "#2563eb", HeaderColor: "#ffffff", HeaderBold: true, BandBG: "#e8f0fe"},
		{Name: "Green header, banded", HeaderBG: "#15803d", HeaderColor: "#ffffff", HeaderBold: true, BandBG: "#e6f4ea"},
		{Name: "Grey banded", HeaderBG: "#374151", HeaderColor: "#ffffff", HeaderBold: true, BandBG: "#f1f3f4"},
		{Name: "Banded, first column bold", HeaderBold: true, BandBG: "#f1f3f4", FirstColBold: true},
	}
}

// SchemeByName finds one of TableSchemes.
func SchemeByName(name string) (TableScheme, bool) {
	for _, s := range TableSchemes() {
		if s.Name == name {
			return s, true
		}
	}
	return TableScheme{}, false
}

// ApplyScheme dresses the table in s: every cell's background, colour and bold are set by it (a covered cell of a merge
// is left, the merge's owner is dressed), while alignment and italics stay.
func (t *Table) ApplyScheme(s TableScheme) {
	for r := range t.Cells {
		for c := range t.Cells[r] {
			if t.Covered(r, c) {
				continue
			}
			st := t.StyleAt(r, c)
			st.Background, st.Color, st.Bold = "", "", false
			if r == 0 {
				st.Background, st.Color, st.Bold = s.HeaderBG, s.HeaderColor, s.HeaderBold
			} else {
				if s.BandBG != "" && r%2 == 0 {
					st.Background = s.BandBG
				}
				if s.FirstColBold && c == 0 {
					st.Bold = true
				}
			}
			t.SetCellStyle(r, c, st)
		}
	}
}

// SchemeOf names the style the table is dressed in now: the one whose ApplyScheme would change nothing. "" when none
// matches (a table dressed by hand). A table with no formatting at all is "Plain".
func (t *Table) SchemeOf() string {
	for _, s := range TableSchemes() {
		probe := &Table{Cells: t.Cells, Merges: t.Merges, Styles: map[CellPos]CellStyle{}}
		for k, v := range t.Styles {
			probe.Styles[k] = v
		}
		probe.ApplyScheme(s)
		if sameStyles(probe.Styles, t.Styles) {
			return s.Name
		}
	}
	return ""
}

func sameStyles(a, b map[CellPos]CellStyle) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
