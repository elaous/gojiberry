package export

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/pdftext"
)

var lineBreaks = regexp.MustCompile(`(\d)(w\d)`)

func widowSnapshot() Snapshot {
	var blocks []Block
	for i := 0; i < 40; i++ {
		words := 14 + (i*7)%60
		blocks = append(blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: strings.TrimSpace(strings.Repeat(fmt.Sprintf("w%02d ", i), words))}}})
	}
	return Snapshot{Title: "T", Lang: "en", Blocks: blocks, PageWidth: 600, PageHeight: 700, MarginLeft: 60, MarginTop: 70, MarginRight: 60, MarginBottom: 70}
}

func TestPDFNeverLeavesAWidowOrAnOrphanLine(t *testing.T) {
	data, err := PDF{}.Export(widowSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	for i := range pages {
		pages[i] = lineBreaks.ReplaceAllString(pages[i], "$1\n$2")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 3 {
		t.Fatalf("the document should run to several pages, got %d", len(pages))
	}
	split := 0
	for pi, txt := range pages {
		lines := map[string]int{}
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

func TestPDFTableNeverLeavesALoneRowAtAPageEdge(t *testing.T) {
	const rows = 9
	rowRe := regexp.MustCompile(`hdr|row\d\d`)
	split := 0
	for filler := 0; filler < 40; filler++ {
		var blocks []Block
		for i := 0; i < filler; i++ {
			blocks = append(blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: "filler"}}})
		}
		for r := 0; r < rows; r++ {
			text := fmt.Sprintf("row%02d", r)
			if r == 0 {
				text = "hdr"
			}
			blocks = append(blocks, Block{Kind: TableCell, TableRow: r, TableRows: rows, TableCols: 1, TableColSpan: 1, TableRowSpan: 1, Segments: []Segment{{Text: text}}})
		}
		snap := Snapshot{Title: "T", Lang: "en", Blocks: blocks, PageWidth: 600, PageHeight: 700, MarginLeft: 60, MarginTop: 70, MarginRight: 60, MarginBottom: 70}
		data, err := PDF{}.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		pages, err := pdftext.ExtractPagesBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		var counts []int
		for _, txt := range pages {
			if n := len(rowRe.FindAllString(txt, -1)); n > 0 {
				counts = append(counts, n)
			}
		}
		if len(counts) > 1 {
			split++
		}
		for _, n := range counts {
			if n < 2 {
				t.Errorf("%d filler lines: rows per page %v: a page holds one row of the table", filler, counts)
				break
			}
		}
	}
	if split == 0 {
		t.Error("the table was never split over a page: the test does not exercise the rule")
	}
}
