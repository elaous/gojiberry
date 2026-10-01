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

func TestTextStyleCSS(t *testing.T) {
	got := TextStyle{
		FontFamily: `Georgia, serif`, FontSize: 20, FontWeight: 700, FontStyle: StyleItalic, LineHeight: 30,
		Color: "#123456", Background: "#ff0", Underline: true, Strike: true,
	}.CSS()
	want := "font-family:Georgia, serif;font-size:20px;font-weight:700;font-style:italic;line-height:30px;" +
		"color:#123456;background-color:#ff0;text-decoration:underline line-through;"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := (TextStyle{FontSize: 10, Superscript: true}).CSS(); got != "font-size:7px;vertical-align:super;" {
		t.Fatalf("super: %s", got)
	}
	if got := (TextStyle{Subscript: true}).CSS(); got != "font-size:11.2px;vertical-align:sub;" {
		t.Fatalf("sub: %s", got)
	}
	if got := (TextStyle{}).CSS(); got != "font-size:16px;" {
		t.Fatalf("zero: %s", got)
	}
	if got := (Format{FontWeight: 700}).Resolve(TextStyle{FontSize: 12}); got.FontWeight != 700 || got.FontSize != 12 {
		t.Fatalf("Resolve = %+v", got)
	}
}
