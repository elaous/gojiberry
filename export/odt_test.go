package export

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/transfer"
)

func odtSnapshot(t *testing.T) Snapshot {
	snap := taggedSnapshot(t)
	snap.Header, snap.Footer = "Left\tMid {title}\tPage {page} of {pages}", "{page}"
	snap.Lang = "en"
	snap.TOC, snap.TOCTitle = true, "Inhalt"
	snap.Blocks = append([]Block{
		{Kind: Paragraph, Segments: []Segment{{Text: "bold ", Format: Format{Bold: true}}, {Text: "link", Format: Format{Href: "https://example.org"}}, {Text: " note", Format: Format{NoteRef: 1}}, {Text: " bonjour", Format: Format{Lang: "fr"}}, {SoftBreak: true}, {Text: "x2", Format: Format{Superscript: true}}}},
		{Kind: BulletItem, Segments: []Segment{{Text: "outer"}}},
		{Kind: BulletItem, Level: 1, Segments: []Segment{{Text: "inner"}}},
		{Kind: NumberedItem, NumberedIndex: 1, Segments: []Segment{{Text: "first"}}},
		{Kind: NumberedItem, NumberedIndex: 2, Segments: []Segment{{Text: "second"}}},
		{Kind: Quote, Segments: []Segment{{Text: "quoted"}}},
		{Kind: Code, Segments: []Segment{{Text: "func main() {\n\treturn\n}"}}},
	}, snap.Blocks...)
	snap.Blocks = append(snap.Blocks, Block{Kind: NumberedItem, Note: 1, NumberedIndex: 1, Segments: []Segment{{Text: "the note"}}})
	return snap
}

func unzipAll(t *testing.T, data []byte) map[string][]byte {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = b
	}
	return out
}

func TestODTIsAnOpenDocumentPackage(t *testing.T) {
	data, err := ODT{}.Export(odtSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		t.Error("the mimetype must be the first entry and stored")
	}
	files := unzipAll(t, data)
	if string(files["mimetype"]) != "application/vnd.oasis.opendocument.text" {
		t.Errorf("mimetype %q", files["mimetype"])
	}
	content := string(files["content.xml"])
	for _, want := range []string{`<text:h text:style-name="Heading_20_1" text:outline-level="1">`, `xlink:href="https://example.org"`, `<text:note text:id="ftn1"`, `fo:language="fr"`,
		`text:style-name="LB"`, `text:style-name="LN"`, `<draw:image xlink:href="Pictures/image1.png"`, `<svg:title>A square</svg:title>`, `table:number-columns-repeated="2"`, `<text:line-break/>`, `style:text-position="super 58%"`} {
		if !strings.Contains(content, want) {
			t.Errorf("content.xml lacks %s", want)
		}
	}
	if strings.Contains(content, "the note</text:p></text:list-item>") {
		t.Error("the note must be a footnote, not a list item")
	}
	if !strings.Contains(string(files["styles.xml"]), `<text:page-number text:select-page="current">`) || !strings.Contains(string(files["meta.xml"]), "<dc:language>en</dc:language>") {
		t.Error("header fields and the language are written")
	}
	if len(files["Pictures/image1.png"]) == 0 || !strings.Contains(string(files["META-INF/manifest.xml"]), `Pictures/image1.png`) {
		t.Error("the picture is in the package and the manifest")
	}
}

// With the ODF 1.2 schema (ODF_SCHEMA=path to OpenDocument-v1.2-os-schema.rng) and xmllint installed, the parts are
// validated against it. It is how the writer was checked, since no office suite runs where it was written.
func TestODTValidatesAgainstTheOpenDocumentSchema(t *testing.T) {
	schema := os.Getenv("ODF_SCHEMA")
	lint, err := exec.LookPath("xmllint")
	if schema == "" || err != nil {
		t.Log("SKIPPED, NOT TESTED: set ODF_SCHEMA to the ODF 1.2 RelaxNG schema and install xmllint")
		return
	}
	plain := odtSnapshot(t)
	themed := odtSnapshot(t)
	themed.Theme = testTheme
	for label, snap := range map[string]Snapshot{"plain": plain, "themed": themed} {
		data, err := ODT{}.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		for name, b := range unzipAll(t, data) {
			if name != "content.xml" && name != "styles.xml" && name != "meta.xml" {
				continue
			}
			f := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(f, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(lint, "--noout", "--relaxng", schema, f).CombinedOutput(); err != nil {
				t.Errorf("%s %s: %v\n%s", label, name, err, out)
			}
		}
	}
}

func TestODTWeWriteIsReadBack(t *testing.T) {
	data, err := ODT{}.Export(odtSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	r := transfer.Default()
	Register(r)
	doc, err := r.Import("odt", "a.odt", data)
	if err != nil {
		t.Fatal(err)
	}
	var txt strings.Builder
	var link, bold, sup, fr, nested, ordered, image, table, quote bool
	for _, it := range doc.Content {
		switch {
		case it.Chars != nil:
			c := it.Chars
			txt.WriteString(c.Text)
			link = link || c.Format.Link == "https://example.org"
			bold = bold || (strings.HasPrefix(c.Text, "bold") && c.Format.FontWeight == 700)
			sup = sup || (c.Text == "x2" && c.Format.Superscript)
			fr = fr || (strings.Contains(c.Text, "bonjour") && c.Format.Lang == "fr")
		case it.Para != nil:
			nested = nested || it.Para.List.Level == 1
			ordered = ordered || it.Para.List.Kind == 2
			quote = quote || it.Para.QuoteBar != ""
		case it.Image != nil:
			image = image || it.Image.Alt == "A square" && strings.HasPrefix(it.Image.URL, "data:image/png;base64,")
		case it.Table != nil:
			table = len(it.Table.Rows) == 2 && it.Table.Rows[0].Cells[0] == "cell"
		}
	}
	for _, want := range []string{"Title", "bold link", "outer", "inner", "first", "second", "quoted", "func main() {\n\treturn\n}", "Body text."} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("text lacks %q: %q", want, txt.String())
		}
	}
	if !link || !bold || !sup || !fr || !nested || !ordered || !image || !table || !quote {
		t.Errorf("link %v bold %v sup %v fr %v nested %v ordered %v image %v table %v quote %v", link, bold, sup, fr, nested, ordered, image, table, quote)
	}
	if len(doc.Footnotes) != 1 || doc.Footnotes[0].Text != "the note" {
		t.Errorf("footnotes: %+v", doc.Footnotes)
	}
	rep := ImportFidelity("odt", data)
	if !rep.Known {
		t.Fatal("odt import reports are known")
	}
	hf := 0
	for _, l := range rep.Losses {
		if l.Feature == FeatHeaderFooter {
			hf = l.Count
		}
	}
	if hf != 2 {
		t.Errorf("the header and footer we wrote are reported as left out: %+v", rep.Losses)
	}
	if _, err := r.Import("odt", "a.odt", []byte("not a zip")); err == nil {
		t.Error("garbage is refused")
	}
}

// A table of contents is a real ODF index: a title, an entry for each heading linked to a bookmark on it, and
// a page break after it.
func TestODTHasATableOfContentsOfItsHeadings(t *testing.T) {
	snap := odtSnapshot(t)
	data, err := ODT{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	content := string(unzipAll(t, data)["content.xml"])
	if !strings.Contains(content, `<text:table-of-content `) || !strings.Contains(content, `<text:index-title-template text:style-name="Contents_20_Heading">Inhalt<`) || !strings.Contains(content, `<text:p text:style-name="Page_20_Break"/>`) {
		t.Fatalf("no index in the document:\n%.800s", content)
	}
	entries := snap.Outline()
	if len(entries) == 0 {
		t.Fatal("the sample has no headings")
	}
	for _, e := range entries {
		name := "__Toc_" + strconv.Itoa(e.Block)
		if !strings.Contains(content, `xlink:href="#`+name+`">`) || !strings.Contains(content, `<text:bookmark-start text:name="`+name+`"/>`) || !strings.Contains(content, `<text:bookmark-end text:name="`+name+`"/>`) {
			t.Errorf("the entry %q is not linked to a bookmark %s", e.Text, name)
		}
	}
	if !strings.Contains(content, `text:style-name="Contents_20_1"`) || !strings.Contains(string(unzipAll(t, data)["styles.xml"]), `style:name="Contents_20_1"`) {
		t.Errorf("the entry style is used but not defined")
	}
	// the index comes first, before the first heading
	if strings.Index(content, "<text:table-of-content ") > strings.Index(content, "<text:h ") {
		t.Errorf("the contents are not at the start")
	}
	snap.TOC = false
	off, _ := ODT{}.Export(snap)
	if strings.Contains(string(unzipAll(t, off)["content.xml"]), "table-of-content") {
		t.Error("contents written although not asked for")
	}
}

// The contents index is made from the headings, so reading the file back does not turn its entries into text:
// every heading is in the document once.
func TestODTContentsAreNotReadBackAsText(t *testing.T) {
	snap := odtSnapshot(t)
	data, err := ODT{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	r := transfer.Default()
	Register(r)
	doc, err := r.Import("odt", "a.odt", data)
	if err != nil {
		t.Fatal(err)
	}
	var txt strings.Builder
	for _, it := range doc.Content {
		if it.Chars != nil {
			txt.WriteString(it.Chars.Text + "\n")
		}
	}
	for _, e := range snap.Outline() {
		if n := strings.Count(txt.String(), e.Text); n != 1 {
			t.Errorf("heading %q is in the document %d times after the round trip", e.Text, n)
		}
	}
	if strings.Contains(txt.String(), "Inhalt") {
		t.Errorf("the contents title was read back as text")
	}
}
