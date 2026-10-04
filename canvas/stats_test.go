package canvas

import (
	"testing"
	"time"
)

func TestCountText(t *testing.T) {
	cases := []struct {
		in   string
		want TextStats
	}{
		{"", TextStats{}},
		{"Hello, world.", TextStats{Words: 2, Characters: 13, CharactersNoSpaces: 12, Paragraphs: 1, Sentences: 1}},
		{"One. Two! Three?\n\nFour", TextStats{Words: 4, Characters: 20, CharactersNoSpaces: 18, Paragraphs: 2, Sentences: 4}},
		{"a b \"quoted.\"   c", TextStats{Words: 4, Characters: 15, CharactersNoSpaces: 12, Paragraphs: 1, Sentences: 2}},
		{"   \n  ", TextStats{Characters: 5}},
		{"x￼y", TextStats{Words: 2, Characters: 2, CharactersNoSpaces: 2, Paragraphs: 1, Sentences: 1}},
	}
	for _, c := range cases {
		if got := CountText(c.in); got != c.want {
			t.Errorf("CountText(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestReadingTime(t *testing.T) {
	if got := (TextStats{Words: 238}).ReadingTime(); got != time.Minute {
		t.Errorf("238 words = %v", got)
	}
}

func TestFlowStatsSelection(t *testing.T) {
	f := NewTextFlow("one two three", 0, 0, 300, 200)
	f.SetPainter(&fakePainter{})
	f.Draw()
	st := f.Stats()
	if st.Text.Words != 3 || st.HasSelection {
		t.Fatalf("stats = %+v", st)
	}
	f.SetSelection(0, 7)
	st = f.Stats()
	if !st.HasSelection || st.Selection.Words != 2 || st.Selection.Characters != 7 {
		t.Errorf("selection stats = %+v", st)
	}
}
