package export_test

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/tokens"
)

// What tokens writes is exactly what the HTML export accepts.
func TestHTMLAcceptsTheTokensPackagesCSS(t *testing.T) {
	set := tokens.Default()
	set.Colors["brand"] = "#336699"
	css, err := set.CSS()
	if err != nil {
		t.Fatal(err)
	}
	out, err := export.HTML{Theme: css}.Export(export.Snapshot{Title: "T"})
	if err != nil || !strings.Contains(string(out), "--gj-color-brand:#336699;") {
		t.Fatalf("%v\n%s", err, out)
	}
}
