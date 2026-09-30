package canvas

import "testing"

func TestTextStyleFont(t *testing.T) {
	tests := []struct {
		s    TextStyle
		want string
	}{
		{TextStyle{}, "normal 400 16px sans-serif"},
		{DefaultTextStyle(), "normal 400 16px sans-serif"},
		{TextStyle{FontFamily: `Georgia, "Times New Roman", serif`, FontSize: 18.5, FontWeight: 700, FontStyle: StyleItalic}, `italic 700 18.5px Georgia, "Times New Roman", serif`},
		{TextStyle{FontFamily: "Menlo", FontSize: 12, FontWeight: 300, FontStyle: StyleOblique}, "oblique 300 12px Menlo"},
	}
	for _, tt := range tests {
		if got := tt.s.Font(); got != tt.want {
			t.Errorf("Font() = %q, want %q", got, tt.want)
		}
	}
}

func TestTextStyleLeading(t *testing.T) {
	if got := (TextStyle{LineHeight: 30}).Leading(); got != 30 {
		t.Errorf("explicit leading = %g", got)
	}
	if got := (TextStyle{FontSize: 20}).Leading(); got != 28 {
		t.Errorf("default leading = %g, want 1.4 x size", got)
	}
	if got := (TextStyle{}).Leading(); got != 1.4*16 {
		t.Errorf("zero style leading = %g", got)
	}
}
