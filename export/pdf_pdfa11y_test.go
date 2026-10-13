package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tagged PDF is checked by pdfa11y (github.com/speedata/pdfa11y, MIT), a Go checker for the
// machine-checkable part of PDF/UA (the Matterhorn protocol). It is a command, so it is run when it is
// installed: `make pdfa11y-test` installs it and requires it (GOJIBERRY_REQUIRE_PDFA11Y=1). It does not
// replace a veraPDF run, which is the reference validator.
func TestPdfa11yPassesTheExports(t *testing.T) {
	tool, err := exec.LookPath("pdfa11y")
	if err != nil {
		if os.Getenv("GOJIBERRY_REQUIRE_PDFA11Y") != "" {
			t.Fatal("pdfa11y is required here and is not installed (make pdfa11y-test installs it)")
		}
		t.Log("SKIPPED, NOT TESTED: pdfa11y is not installed (make pdfa11y-test)")
		return
	}
	link := func(s string) []Segment {
		return []Segment{{Text: "visit "}, {Text: s, Format: Format{Href: "https://example.org"}}, {Text: " bonjour", Format: Format{Lang: "fr"}}}
	}
	docs := map[string]Snapshot{
		"tagged": taggedSnapshot(t),
		"text": textSnapshot(
			Block{Kind: Heading1, Segments: []Segment{{Text: "Title"}}},
			Block{Kind: Heading2, Segments: []Segment{{Text: "Part"}}},
			Block{Kind: Paragraph, Segments: link("here")},
			Block{Kind: Paragraph, Segments: []Segment{{Text: "bold", Format: Format{Bold: true}}, {Text: " and italic", Format: Format{Italic: true}}, {Text: " both", Format: Format{Bold: true, Italic: true}}}},
			Block{Kind: BulletItem, Segments: []Segment{{Text: "outer"}}},
			Block{Kind: BulletItem, Level: 1, Segments: []Segment{{Text: "inner"}}},
			Block{Kind: NumberedItem, Segments: []Segment{{Text: "first"}}},
			Block{Kind: NumberedItem, Segments: []Segment{{Text: "second"}}},
		),
	}
	for name, snap := range docs {
		data, err := PDF{}.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), name+".pdf")
		if err := os.WriteFile(f, data, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(tool, f).CombinedOutput()
		if err != nil {
			var bad []string
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "FAIL") || strings.Contains(l, "↳") || strings.Contains(l, "WARN") {
					bad = append(bad, l)
				}
			}
			t.Errorf("%s: pdfa11y: %v\n%s", name, err, strings.Join(bad, "\n"))
		}
	}
}
