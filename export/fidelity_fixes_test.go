package export

import (
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

func bullet(level int) docfile.Item {
	return paraItem(docfile.Para{List: docfile.List{Kind: 1, Level: level}})
}

func numbered(level int) docfile.Item {
	return paraItem(docfile.Para{List: docfile.List{Kind: 2, Level: level}})
}

// A list item's nesting level reaches the exporters (it was dropped on the way to the Snapshot, so every nested
// list was written flat), and the numbers count within each level.
func TestNestedListsKeepTheirLevelAndNumbering(t *testing.T) {
	d := docOf(numbered(0), plain("one\n"), numbered(1), plain("one-a\n"), numbered(1), plain("one-b\n"),
		bullet(1), plain("dot\n"), numbered(0), plain("two\n"), numbered(1), plain("two-a\n"), plain("end\n"))
	snap := FromDocument(d)
	var got []string
	for _, b := range snap.Blocks {
		if b.Kind == NumberedItem || b.Kind == BulletItem {
			got = append(got, strings.Repeat(">", b.Level)+itoa(b.NumberedIndex))
		}
	}
	if want := "1 >1 >2 >0 2 >1"; strings.Join(got, " ") != want {
		t.Errorf("levels and numbers = %q, want %q", strings.Join(got, " "), want)
	}
	html, _ := HTML{}.Export(snap)
	if !strings.Contains(string(html), "<li>one<ol>\n<li>one-a</li><li>one-b</li></ol>\n<ul>\n<li>dot</li></ul>\n</li><li>two<ol>") {
		t.Errorf("the nested list is not nested in the HTML:\n%s", html)
	}
	md, _ := Markdown{}.Export(snap)
	if !strings.Contains(string(md), "1. one\n\n  1. one-a\n\n  2. one-b\n") {
		t.Errorf("the nested list is flat in Markdown:\n%s", md)
	}
	docx := string(unzipEntry(t, rawOne(t, registry(), "docx", d), "word/document.xml"))
	if !strings.Contains(docx, `<w:ilvl w:val="1"/>`) {
		t.Errorf("the nested list is flat in Word")
	}
}

// Text proposed to be removed must not reach the Markdown or plain text of the editor's own exporters, which
// cannot strike it: they write the document as it would be with every suggestion accepted.
func TestMarkdownAndTextWriteSuggestionsAsAccepted(t *testing.T) {
	del := canvas.ProposalMark{Kind: canvas.ProposalDelete, ID: "s1", Author: "Ann", Time: time.Unix(1700000000, 0)}.String()
	ins := canvas.ProposalMark{Kind: canvas.ProposalInsert, ID: "s1", Author: "Ann", Time: time.Unix(1700000000, 0)}.String()
	d := docOf(plain("The "), ch("old", docfile.Format{Proposal: del}), ch("new", docfile.Format{Proposal: ins}), plain(" text\n"))
	r := registry()
	for _, format := range []string{"markdown", "text"} {
		out := string(rawOne(t, r, format, d))
		if strings.Contains(out, "old") || !strings.Contains(out, "The new text") {
			t.Errorf("%s: %q: the removed text is in the file, or the added text is not", format, out)
		}
	}
	// The formats that can show a suggestion still do.
	if out := string(exportOne(t, r, "html", d)); !strings.Contains(out, "<del") || !strings.Contains(out, "old") {
		t.Errorf("html lost the tracked deletion: %s", out)
	}
}

// The editor's heading command sets a paragraph style, not a size on every run; Markdown writes it as a heading.
func TestMarkdownWritesStyleHeadings(t *testing.T) {
	d := docOf(paraItem(docfile.Para{Style: "Heading 2"}), plain("Section\n"), paraItem(docfile.Para{Style: "Title"}), plain("Name\n"), plain("body\n"))
	out := string(rawOne(t, registry(), "markdown", d))
	if out != "## Section\n# Name\nbody\n" {
		t.Errorf("Markdown = %q", out)
	}
}
