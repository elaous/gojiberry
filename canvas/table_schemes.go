package canvas

import (
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Table styles: a named look for a whole table (a header row in a colour, bands on every second row, bold first
// column) applied in one step. A style is applied as cell formatting (CellStyle), so it is saved with the table,
// written by every export that writes cell shading and colour, and can still be changed cell by cell afterwards.
// Alignment and italics a cell already has are kept; background, colour and bold are set by the style.

// TableScheme is a named look for a table.
type TableScheme struct {
	Name string `json:"name"`
	// HeaderBG and HeaderColor dress the first row; HeaderBold makes it bold.
	HeaderBG    string `json:"headerBG,omitempty"`
	HeaderColor string `json:"headerColor,omitempty"`
	HeaderBold  bool   `json:"headerBold,omitempty"`
	// BandBG is the background of every second row of the body (the first body row is plain, the second banded...).
	BandBG string `json:"bandBG,omitempty"`
	// FirstColBold makes the first column of the body bold.
	FirstColBold bool `json:"firstColBold,omitempty"`
}

// TableSchemes are the styles the editor offers, the first (Plain) taking every style off: the ones built in, then the
// ones the person defined (see AddTableScheme).
func TableSchemes() []TableScheme {
	return append(builtinSchemes(), CustomTableSchemes()...)
}

func builtinSchemes() []TableScheme {
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

// keepScheme runs a change that adds, removes, moves or sorts rows or columns. A table that was dressed in one of the
// styles (and only in it: SchemeOf says so) is dressed again afterwards, so the bands and the bold first column follow
// the rows instead of staying where they were. A table dressed by hand is left as the change made it.
func (t *Table) keepScheme(change func()) {
	name := t.SchemeOf()
	change()
	if name == "" || name == "Plain" || len(t.Styles) == 0 { // (a table cleared down to one cell has lost its dress for good)
		return
	}
	if s, ok := SchemeByName(name); ok {
		t.ApplyScheme(s)
	}
}

// The styles a person defines are kept for the document being edited: the host loads them when a document is opened
// (SetCustomTableSchemes) and saves CustomTableSchemes with it. They are in one list for the page, as the fonts are.
var (
	customMu      sync.RWMutex
	customSchemes []TableScheme
)

// MaxCustomSchemes is how many styles of their own a document can hold.
const MaxCustomSchemes = 20

var reHexColour = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// CustomTableSchemes lists the styles defined by the person.
func CustomTableSchemes() []TableScheme {
	customMu.RLock()
	defer customMu.RUnlock()
	return append([]TableScheme(nil), customSchemes...)
}

// Valid says whether the style can be kept: a name (at most 40 characters) that is not one of the built-in ones and
// colours that are empty or written as #rgb or #rrggbb.
func (s TableScheme) Valid() error {
	name := strings.TrimSpace(s.Name)
	switch {
	case name == "" || len([]rune(name)) > 40:
		return errors.New("a style needs a name of up to 40 characters")
	}
	for _, b := range builtinSchemes() {
		if strings.EqualFold(b.Name, name) {
			return errors.New("that name is one of the built-in styles")
		}
	}
	for _, c := range []string{s.HeaderBG, s.HeaderColor, s.BandBG} {
		if c != "" && !reHexColour.MatchString(c) {
			return errors.New("colours are written like #2563eb")
		}
	}
	return nil
}

// AddTableScheme keeps a style the person defined, replacing one of the same name.
func AddTableScheme(s TableScheme) error {
	s.Name = strings.TrimSpace(s.Name)
	if err := s.Valid(); err != nil {
		return err
	}
	customMu.Lock()
	defer customMu.Unlock()
	for i, c := range customSchemes {
		if strings.EqualFold(c.Name, s.Name) {
			customSchemes[i] = s
			return nil
		}
	}
	if len(customSchemes) >= MaxCustomSchemes {
		return errors.New("there are as many styles of your own as a document can hold")
	}
	customSchemes = append(customSchemes, s)
	return nil
}

// RemoveTableScheme forgets a style the person defined (a table dressed in it stays as it is dressed).
func RemoveTableScheme(name string) {
	customMu.Lock()
	defer customMu.Unlock()
	for i, c := range customSchemes {
		if c.Name == name {
			customSchemes = append(customSchemes[:i:i], customSchemes[i+1:]...)
			return
		}
	}
}

// SetCustomTableSchemes replaces the person's styles (when a document is opened); a style that is not valid is left out.
func SetCustomTableSchemes(list []TableScheme) {
	customMu.Lock()
	customSchemes = nil
	customMu.Unlock()
	for _, s := range list {
		_ = AddTableScheme(s)
	}
}
