package canvas

// TableStyle controls how a TableView looks and is laid out.
type TableStyle struct {
	Font        string // body cell font
	HeaderFont  string // header row font
	TextColor   string
	HeaderBG    string
	CellBG      string
	GridColor   string
	ActiveColor string // border of the cell being edited, and the caret

	PadX          float64 // horizontal cell padding
	RowHeight     float64
	MinColWidth   float64
	BarSize       float64 // thickness of the "+" bars
	BarGap        float64 // space between the table and each bar
	BarColor      string
	BarHoverColor string
	BarTextColor  string
	BarFont       string

	HandleSize      float64 // thickness of the column/row/table handles
	SelectColor     string  // translucent fill over a selected column/row/table
	HandleIconColor string

	FormulaColor string // the text of a formula (shown when the formulas are), red
	ValueColor   string // the value a formula computed, blue
}

// DefaultTableStyle is a light, Obsidian-like look.
func DefaultTableStyle() TableStyle {
	return TableStyle{
		Font:        "15px sans-serif",
		HeaderFont:  "bold 15px sans-serif",
		TextColor:   "#2e3338",
		HeaderBG:    "#f2f3f5",
		CellBG:      "#ffffff",
		GridColor:   "#d4d6da",
		ActiveColor: "#7c5cff",

		PadX:          10,
		RowHeight:     32,
		MinColWidth:   80,
		BarSize:       22,
		BarGap:        4,
		BarColor:      "#eceef1",
		BarHoverColor: "#d9dce2",
		BarTextColor:  "#5b616b",
		BarFont:       "bold 16px sans-serif",

		HandleSize:      16,
		SelectColor:     "rgba(124, 92, 255, 0.14)",
		HandleIconColor: "#7b818b",

		FormulaColor: "#c01c28",
		ValueColor:   "#1c5fb4",
	}
}

// DarkTableStyle is DefaultTableStyle for a dark page.
func DarkTableStyle() TableStyle {
	s := DefaultTableStyle()
	s.TextColor, s.HeaderBG, s.CellBG, s.GridColor = "#dcddde", "#2b2b2b", "#1e1e1e", "#3a3a3a"
	s.BarColor, s.BarHoverColor, s.BarTextColor = "#2f2f2f", "#3d3d3d", "#a8a8a8"
	s.SelectColor, s.HandleIconColor = "rgba(124, 92, 255, 0.25)", "#8a8a8a"
	s.FormulaColor, s.ValueColor = "#ff8a80", "#8ab4f8"
	return s
}
