package canvas

import "testing"

// CaretFormat is the format the next typed text takes.
func TestCaretFormatIsWhatTheNextTextWouldTake(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "plain ", Format: Format{}}, Chars{Text: "bold", Format: Format{FontWeight: 700}})
	f.SetCursors([]Cursor{At(3)})
	if fm := f.CaretFormat(); fm.FontWeight != 0 {
		t.Errorf("in plain text: %+v", fm)
	}
	f.SetCursors([]Cursor{At(8)})
	if fm := f.CaretFormat(); fm.FontWeight != 700 {
		t.Errorf("in bold text: %+v", fm)
	}
	f.SetCursors([]Cursor{At(3)})
	st.HandleChord("Mod+B")
	if fm := f.CaretFormat(); fm.FontWeight != 700 {
		t.Errorf("Ctrl+B with nothing selected is pending: %+v", fm)
	}
}
