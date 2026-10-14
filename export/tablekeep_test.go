package export

import (
	"strings"
	"testing"
)

func keepTable(rows int) Snapshot {
	var blocks []Block
	for r := 0; r < rows; r++ {
		blocks = append(blocks, Block{Kind: TableCell, TableRow: r, TableRows: rows, TableCols: 1, TableColSpan: 1, TableRowSpan: 1, Segments: []Segment{{Text: "row"}}})
	}
	return Snapshot{Title: "T", Blocks: blocks, PageWidth: 600, PageHeight: 800, MarginLeft: 60, MarginTop: 70, MarginRight: 60, MarginBottom: 70}
}

func TestKeepRowWithNext(t *testing.T) {
	for _, c := range []struct {
		r, rows int
		want    bool
	}{{0, 1, false}, {0, 2, true}, {1, 2, false}, {0, 4, true}, {1, 4, false}, {2, 4, true}, {3, 4, false}} {
		if got := keepRowWithNext(c.r, c.rows); got != c.want {
			t.Errorf("row %d of %d: %v", c.r, c.rows, got)
		}
	}
}

// A table's rows are not split across pages, and its header row and the row before the last stay with the row
// after them, so no row is left alone at the foot or the head of a page, in each paged format.
func TestTableRowsAreKeptTogetherInEveryFormat(t *testing.T) {
	snap := keepTable(4)
	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	doc := docxParts(t, data)["word/document.xml"]
	if strings.Count(doc, "<w:cantSplit/>") != 4 || strings.Count(doc, "<w:keepNext/>") != 2 {
		t.Errorf("Word: %d cantSplit, %d keepNext", strings.Count(doc, "<w:cantSplit/>"), strings.Count(doc, "<w:keepNext/>"))
	}
	rtf, _ := RTF{}.Export(snap)
	if s := string(rtf); strings.Count(s, `\trkeep`) != 4 || strings.Count(s, `\pard\intbl\keepn`) != 2 {
		t.Errorf("RTF: %d trkeep, %d keepn", strings.Count(s, `\trkeep`), strings.Count(s, `\pard\intbl\keepn`))
	}
	odt, _ := ODT{}.Export(snap)
	content := docxParts(t, odt)["content.xml"]
	if strings.Count(content, `table:style-name="TableRow"`) != 4 || !strings.Contains(content, `fo:keep-together="always"`) || !strings.Contains(content, `fo:keep-with-next="always"`) {
		t.Errorf("ODT: rows or styles lack the keep rules")
	}
	html, _ := HTML{}.Export(snap)
	if s := string(html); !strings.Contains(s, "thead{display:table-header-group;break-after:avoid}") || !strings.Contains(s, "tbody tr:last-child{break-before:avoid}") {
		t.Error("HTML print stylesheet")
	}
}
