package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func TestHTMLCarriesTheDocumentsOwnTheme(t *testing.T) {
	d := docfile.Document{Title: "T", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "hello\n"}}}}
	d.SetTheme(":root{--gj-color-text:#111111;--gj-font-1:18px;}")
	out, err := HTML{}.Export(FromDocument(d))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "--gj-color-text:#111111") {
		t.Errorf("the document's theme is not in the page:\n%s", out)
	}
	// An exporter's own theme wins; an unsafe stored theme is left out, not an error.
	out, _ = HTML{Theme: ":root{--gj-color-text:#222222;}"}.Export(FromDocument(d))
	if strings.Contains(string(out), "#111111") || !strings.Contains(string(out), "#222222") {
		t.Error("the exporter's theme should win")
	}
	d.SetTheme(":root{}</style><script>alert(1)</script>")
	out, err = HTML{}.Export(FromDocument(d))
	if err != nil || strings.Contains(string(out), "<script>alert") {
		t.Errorf("an unsafe stored theme must be dropped: %v\n%s", err, out)
	}
	d.SetTheme("")
	if d.Theme() != "" {
		t.Error("SetTheme(\"\") should remove it")
	}
}
