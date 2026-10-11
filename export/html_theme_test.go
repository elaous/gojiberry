package export

import (
	"strings"
	"testing"
)

const goodTheme = ":root{--gj-color-text:#1a1a1a;--gj-color-background:#ffffff;--gj-font-1:16px;--gj-space-1:4px;}"

func TestHTMLThemeIsWrittenInAStyle(t *testing.T) {
	snap := Snapshot{Title: "T", Blocks: []Block{{Kind: Paragraph, Segments: []Segment{{Text: "hello"}}}}}
	out, err := HTML{Theme: goodTheme}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	i, j := strings.Index(s, "<style>"), strings.Index(s, "</style>")
	if i < 0 || j < i || !strings.Contains(s[i:j], goodTheme) || !strings.Contains(s[i:j], "color:var(--gj-color-text") {
		t.Fatalf("%s", s)
	}
	if j > strings.Index(s, "</head>") || i < strings.Index(s, "<title>") {
		t.Fatal("the style sits in the head")
	}
	plain, _ := HTML{}.Export(snap)
	if strings.Contains(string(plain), "<style>") {
		t.Fatal("no theme, no style element")
	}
}

func TestHTMLThemeThatCouldBreakOutIsRefused(t *testing.T) {
	snap := Snapshot{Title: "T"}
	for name, theme := range map[string]string{
		"closes the style":  ":root{--a:b;}</style><script>alert(1)</script>",
		"a second rule":     ":root{--a:b;}body{display:none}",
		"a nested brace":    ":root{--a:{b};}",
		"not a root block":  "body{--a:b;}",
		"an import":         ":root{--a:b;@import 'x';}",
		"not a custom prop": ":root{color:red;}",
		"a quote":           ":root{--a:'b';}",
		"an escape":         ":root{--a:\\3c;}",
		"a newline":         ":root{--a:b;\n--c:d;}",
		"no closing brace":  ":root{--a:b;",
	} {
		if _, err := (HTML{Theme: theme}).Export(snap); err == nil {
			t.Errorf("%s: written", name)
		}
	}
}
