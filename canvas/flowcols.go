package canvas

// SetColumnsAt sets the column layout from an offset until the next config.
func (f *TextFlow) SetColumnsAt(offset int, cfg ColumnConfig) {
	f.materialize()
	off := 0
	out := make([]Item, 0, len(f.Items)+1)
	for _, it := range f.Items {
		switch v := it.(type) {
		case ColumnConfig:
			if off == offset {
				continue
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

// ColumnsAt returns the column layout in force at offset.
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
