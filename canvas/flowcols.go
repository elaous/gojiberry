package canvas

// SetColumnsAt makes cfg the column layout from text offset (in runes) on, until
// the next ColumnConfig: a new region starts there. Any ColumnConfig already at
// exactly that offset is replaced. It is one undoable change.
func (f *TextFlow) SetColumnsAt(offset int, cfg ColumnConfig) {
	f.materialize()
	off := 0
	out := make([]Item, 0, len(f.Items)+1)
	for _, it := range f.Items {
		switch v := it.(type) {
		case ColumnConfig:
			if off == offset {
				continue // replaced
			}
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			off++
		}
		out = append(out, it)
	}
	f.Items = out
	f.replaceRange(offset, offset, []Item{cfg}, false)
	f.changed()
}

// ColumnsAt returns the column layout in force at offset: the last ColumnConfig
// at or before it, or the flow's own Columns and Gap.
func (f *TextFlow) ColumnsAt(offset int) ColumnConfig {
	cfg := ColumnConfig{Widths: f.Columns, Gap: f.Gap}
	off := 0
	for _, it := range f.items() {
		switch v := it.(type) {
		case ColumnConfig:
			if off <= offset {
				cfg = v
			}
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			off++
		}
	}
	return cfg
}
