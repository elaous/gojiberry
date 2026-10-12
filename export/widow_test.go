package export

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/pdftext"
)

var lineBreaks = regexp.MustCompile(`(\d)(w\d)`)

// widowSnapshot is paragraphs of two to six lines each, every word carrying its paragraph's number, so a
// page's text says how many lines of each paragraph it holds.
func widowSnapshot() Snapshot {
	var blocks []Block
	for i := 0; i < 40; i++ {
		words := 14 + (i*7)%60
		blocks = append(blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: strings.TrimSpace(strings.Repeat(fmt.Sprintf("w%02d ", i), words))}}})
	}
	return Snapshot{Title: "T", Lang: "en", Blocks: blocks, PageWidth: 600, PageHeight: 700, MarginLeft: 60, MarginTop: 70, MarginRight: 60, MarginBottom: 70}
}

// TestPDFNeverLeavesAWidowOrAnOrphanLine checks every page: of each paragraph that is split over two
// pages, both pages hold at least two of its lines.
func TestPDFNeverLeavesAWidowOrAnOrphanLine(t *testing.T) {
	data, err := PDF{}.Export(widowSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	for i := range pages {
		pages[i] = lineBreaks.ReplaceAllString(pages[i], "$1\n$2") // the extractor joins lines without a space
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 3 {
		t.Fatalf("the document should run to several pages, got %d", len(pages))
	}
	split := 0
	for pi, txt := range pages {
		lines := map[string]int{} // paragraph word -> lines of it on this page
		for _, line := range strings.Split(txt, "\n") {
			if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(f[0], "w") {
				lines[f[0]]++
			}
		}
		for w, n := range lines {
			total := 0
			for _, other := range pages {
				for _, line := range strings.Split(other, "\n") {
					if f := strings.Fields(line); len(f) > 0 && f[0] == w {
						total++
					}
				}
			}
			if n < total {
				split++
				if n < 2 {
					t.Errorf("page %d holds %d line of %s, which has %d lines in all", pi+1, n, w, total)
				}
			}
		}
	}
	if split == 0 {
		t.Error("no paragraph was split over a page: the test does not exercise the rule")
	}
}
