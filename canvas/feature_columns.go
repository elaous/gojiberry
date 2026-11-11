package canvas

// Columns sets the column count from the caret's paragraph on.
type Columns struct{}

// Command names.
const (
	CmdColumns1    Command = "columns.1"
	CmdColumns2    Command = "columns.2"
	CmdColumns3    Command = "columns.3"
	CmdColumns4    Command = "columns.4"
	CmdColumns21   Command = "columns.2+1"
	CmdColumns12   Command = "columns.1+2"
	CmdGapNarrow   Command = "columns.gap-narrow"
	CmdGapStandard Command = "columns.gap-standard"
	CmdGapWide     Command = "columns.gap-wide"
	// CmdColumnsReset restores equal widths; the column count is unchanged.
	CmdColumnsReset Command = "columns.reset-widths"
)

// ColumnsHost is what the column commands need of their target: TextFlow.
type ColumnsHost interface {
	SetColumnsAt(offset int, cfg ColumnConfig)
	ColumnsAt(offset int) ColumnConfig
}

// DefaultColumnGap is the space between columns the commands start with.
const DefaultColumnGap = 24

// Name implements Feature.
func (Columns) Name() string { return "columns" }

// proportion is a column width ratio; widths scale to fit the page.
const proportion = 1000

// Install implements Feature.
func (Columns) Install(r *Registry) {
	set := func(cfg func(old ColumnConfig) ColumnConfig) CommandFunc {
		return func(c *CommandContext) bool {
			h, ok := c.Target.(ColumnsHost)
			if !ok {
				return false
			}
			start := paragraphStart(c.Target)
			old := h.ColumnsAt(start)
			h.SetColumnsAt(start, cfg(old))
			return true
		}
	}
	gapOf := func(old ColumnConfig) float64 {
		if old.Gap > 0 {
			return old.Gap
		}
		return DefaultColumnGap
	}
	equal := func(n int) func(ColumnConfig) ColumnConfig {
		return func(old ColumnConfig) ColumnConfig {
			if n == 1 {
				return ColumnConfig{Count: 1}
			}
			return ColumnConfig{Count: n, Gap: gapOf(old)}
		}
	}
	ratio := func(ws ...float64) func(ColumnConfig) ColumnConfig {
		return func(old ColumnConfig) ColumnConfig {
			out := make([]float64, len(ws))
			for i, w := range ws {
				out[i] = w * proportion
			}
			return ColumnConfig{Widths: out, Gap: gapOf(old)}
		}
	}
	gap := func(g float64) func(ColumnConfig) ColumnConfig {
		return func(old ColumnConfig) ColumnConfig {
			old.Gap = g
			return old
		}
	}
	r.AddCommand(CmdColumns1, "One column", "", set(equal(1)))
	r.AddCommand(CmdColumns2, "Two columns", "", set(equal(2)))
	r.AddCommand(CmdColumns3, "Three columns", "", set(equal(3)))
	r.AddCommand(CmdColumns4, "Four columns", "", set(equal(4)))
	r.AddCommand(CmdColumns21, "Two columns, wide then narrow", "", set(ratio(2, 1)))
	r.AddCommand(CmdColumns12, "Two columns, narrow then wide", "", set(ratio(1, 2)))
	r.AddCommand(CmdColumnsReset, "Reset column widths", "", set(func(old ColumnConfig) ColumnConfig {
		n := old.Count
		if len(old.Widths) > 0 {
			n = len(old.Widths)
		}
		if n < 2 {
			return old
		}
		return ColumnConfig{Count: n, Gap: gapOf(old)}
	}))
	r.AddCommand(CmdGapNarrow, "Column spacing: narrow", "", set(gap(12)))
	r.AddCommand(CmdGapStandard, "Column spacing: standard", "", set(gap(DefaultColumnGap)))
	r.AddCommand(CmdGapWide, "Column spacing: wide", "", set(gap(48)))
}

// paragraphStart is the offset of the start of the primary cursor's paragraph.
func paragraphStart(t EditTarget) int {
	rs := textRunes(t)
	i := min(t.Cursors()[0].Caret, len(rs))
	for i > 0 && rs[i-1] != '\n' {
		i--
	}
	return i
}
