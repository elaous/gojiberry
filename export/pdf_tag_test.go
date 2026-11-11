package export

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dslipak/pdf"
	"github.com/elaous/gojiberry/pdfua"
)

func taggedSnapshot(t *testing.T) Snapshot {
	t.Helper()
	var png1 bytes.Buffer
	if err := png.Encode(&png1, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1.Bytes())
	seg := func(s string) []Segment { return []Segment{{Text: s}} }
	blocks := []Block{
		{Kind: Heading1, Segments: seg("Title")},
		{Kind: Paragraph, Segments: seg("Body text.")},
		{Kind: BulletItem, Segments: seg("one")},
		{Kind: BulletItem, Segments: seg("two")},
		{Kind: Image, ImageSrc: src, ImageAlt: "A square", ImageW: 20, ImageH: 20},
		{Kind: Image, ImageSrc: src, ImageDecorative: true, ImageW: 20, ImageH: 20},
	}
	for r := 0; r < 2; r++ {
		for c := 0; c < 2; c++ {
			blocks = append(blocks, Block{Kind: TableCell, Segments: seg("cell"), TableRow: r, TableCol: c, TableRows: 2, TableCols: 2, TableColSpan: 1, TableRowSpan: 1})
		}
	}
	return Snapshot{Title: "Doc", Lang: "en", Blocks: blocks, PageWidth: 794, PageHeight: 1123, MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40}
}

func TestPDFExportIsTagged(t *testing.T) {
	data, err := PDF{}.Export(taggedSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if bytes.Contains(data, []byte("7777.00")) {
		t.Log("markers are inside compressed streams")
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	pr, err := r.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := bytes.NewBuffer(nil), 0
	if _, err := txt.ReadFrom(pr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Title", "Body text.", "one", "two", "cell"} {
		if !bytes.Contains(txt.Bytes(), []byte(want)) {
			t.Errorf("text %q lost: %q", want, txt.String())
		}
	}
}

func TestStructureHasListAndTable(t *testing.T) {
	data, err := PDF{}.Export(taggedSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/S /H1", "/S /P", "/S /L ", "/S /LI", "/S /Lbl", "/S /LBody", "/S /Table", "/S /TR", "/S /TH", "/S /TD", "/S /Figure", "/Alt <FEFF"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("structure has no %q", want)
		}
	}
	if bytes.Count(data, []byte("/S /Figure")) != 1 {
		t.Error("the decorative image should be an artifact, not a figure")
	}
}

func TestTaggingSpansPages(t *testing.T) {
	snap := Snapshot{Title: "Long", Lang: "de", PageWidth: 794, PageHeight: 400, MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40}
	for i := 0; i < 80; i++ {
		snap.Blocks = append(snap.Blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: "Line of text that is long enough to be a paragraph."}}})
	}
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if n := bytes.Count(data, []byte("/StructParents")); n < 2 {
		t.Errorf("%d pages carry a parent key, want several", n)
	}
}

func shapeOf(v pdf.Value) string {
	switch v.Kind() {
	case pdf.Array:
		var parts []string
		for i := 0; i < v.Len(); i++ {
			if s := shapeOf(v.Index(i)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case pdf.Dict:
		if v.Key("Type").Kind() == pdf.Name && v.Key("Type").Name() == "OBJR" {
			return "OBJR"
		}
		s := v.Key("S")
		if s.Kind() != pdf.Name {
			return ""
		}
		out := s.Name()
		if l := v.Key("Lang"); l.Kind() == pdf.String {
			out += "[" + l.Text() + "]"
		}
		if kids := shapeOf(v.Key("K")); kids != "" {
			out += "(" + kids + ")"
		}
		return out
	}
	return ""
}

func structureShape(t *testing.T, data []byte) string {
	t.Helper()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return shapeOf(r.Trailer().Key("Root").Key("StructTreeRoot").Key("K"))
}

func textSnapshot(blocks ...Block) Snapshot {
	return Snapshot{Title: "Doc", Lang: "en", Blocks: blocks, PageWidth: 794, PageHeight: 1123, MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40}
}

func plainTextOf(t *testing.T, data []byte) string {
	t.Helper()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	pr, err := r.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	txt, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	return string(txt)
}

func TestLinkAnnotationsAreInTheStructureTree(t *testing.T) {
	link := func(s, href string) Segment { return Segment{Text: s, Format: Format{Href: href}} }
	snap := textSnapshot(Block{Kind: Paragraph, Segments: []Segment{
		{Text: "See "}, link("the docs", "https://example.org/docs"), {Text: " and "}, link("faq", "https://example.org/faq"), {Text: " now."},
	}})
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if got, want := structureShape(t, data), "Document(P(Link(OBJR) Link(OBJR) Link(OBJR) Link(OBJR)))"; got != want {
		t.Errorf("structure = %s, want %s", got, want)
	}
	if n := bytes.Count(data, []byte("/StructParent ")); n != 4 {
		t.Errorf("%d link annotations with a structure key, want 4", n)
	}
	if n := bytes.Count(data, []byte("/Type /OBJR")); n != 4 {
		t.Errorf("%d /OBJR, want 4", n)
	}
	if !bytes.Contains(data, []byte("/Contents (https://example.org/docs)")) || !bytes.Contains(data, []byte("/Alt (https://example.org/faq)")) {
		t.Error("the link annotation and its element should carry the target as their description")
	}
	if txt := plainTextOf(t, data); !strings.Contains(txt, "See the docs and faq now.") {
		t.Errorf("text = %q", txt)
	}
}

func TestPDFUAChecksFailWhenALinkIsLeftOutOfTheTree(t *testing.T) {
	snap := textSnapshot(Block{Kind: Paragraph, Segments: []Segment{{Text: "go", Format: Format{Href: "https://example.org"}}}})
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(data, []byte("/Type /OBJR"), []byte("/Type /XXXX"), 1)
	got, err := pdfua.Check(broken)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range got {
		found = found || f.Rule == "LinkAnnotation"
	}
	if !found {
		t.Errorf("a link annotation without an /OBJR was not reported: %v", got)
	}
}

func TestLinksInCodeBlocksAndAcrossPages(t *testing.T) {
	code := Block{Kind: Code, Segments: []Segment{{Text: "x := 1", Format: Format{Href: "https://example.org/code"}}}}
	data, err := PDF{}.Export(textSnapshot(code, Block{Kind: Paragraph, Segments: []Segment{{Text: "plain"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("/Subtype /Link")) || bytes.Contains(data, []byte("/S /Link")) {
		t.Error("code is drawn without a link, so there is nothing to tag")
	}

	snap := textSnapshot()
	snap.PageHeight = 300
	for i := 0; i < 40; i++ {
		snap.Blocks = append(snap.Blocks, Block{Kind: Paragraph, Segments: []Segment{
			{Text: "A line with "}, {Text: "linked", Format: Format{Href: "https://example.org/" + strconv.Itoa(i)}}, {Text: " text in it."},
		}})
	}
	data, err = PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	if a, l := bytes.Count(data, []byte("/StructParent ")), bytes.Count(data, []byte("/S /Link")); a != 40 || l != 40 {
		t.Errorf("%d annotations and %d Link elements, want 40 of each", a, l)
	}
	if bytes.Count(data, []byte("/StructParents")) < 3 {
		t.Error("the document should span several pages")
	}
}

func TestNestedListsNestInTheStructureTree(t *testing.T) {
	item := func(kind BlockKind, level int, s string) Block {
		return Block{Kind: kind, Level: level, NumberedIndex: 1, Segments: []Segment{{Text: s}}}
	}
	data, err := PDF{}.Export(textSnapshot(
		item(BulletItem, 0, "a"), item(BulletItem, 1, "b"), item(NumberedItem, 2, "c"), item(BulletItem, 1, "d"), item(BulletItem, 0, "e"),
		Block{Kind: Paragraph, Segments: []Segment{{Text: "between"}}},
		item(BulletItem, 0, "f"), item(NumberedItem, 0, "g"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	li := "LI(Lbl LBody)"
	want := "Document(L(LI(Lbl LBody(L(LI(Lbl LBody(L(" + li + "))) " + li + "))) " + li + ") P L(" + li + ") L(" + li + "))"
	if got := structureShape(t, data); got != want {
		t.Errorf("structure =\n%s\nwant\n%s", got, want)
	}
}

func TestRunLanguagesAreTaggedAsSpans(t *testing.T) {
	run := func(s, lang string) Segment { return Segment{Text: s, Format: Format{Lang: lang}} }
	snap := textSnapshot(
		Block{Kind: Paragraph, Segments: []Segment{{Text: "He said "}, run("bonjour tout le monde", "fr"), {Text: " and then "}, run("guten Tag", "de-CH")}},
		Block{Kind: Paragraph, Segments: []Segment{{Text: "plain "}, run("odd", "12")}},
		Block{Kind: Paragraph, Segments: []Segment{{Text: "see "}, {Text: "ici", Format: Format{Href: "https://example.org", Lang: "fr"}}}},
	)
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	want := "Document(P(Span[fr] Span[de-CH]) P P(Link[fr](OBJR)))"
	if got := structureShape(t, data); got != want {
		t.Errorf("structure = %s, want %s", got, want)
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if l := r.Trailer().Key("Root").Key("Lang").Text(); l != "en" {
		t.Errorf("catalog /Lang = %q", l)
	}
	if txt := plainTextOf(t, data); !strings.Contains(txt, "He said bonjour tout le monde and then guten Tag") {
		t.Errorf("text = %q", txt)
	}
}

func TestTableCellsCarryLanguageSpansAndLinks(t *testing.T) {
	cell := func(r, c int, segs ...Segment) Block {
		return Block{Kind: TableCell, Segments: segs, TableRow: r, TableCol: c, TableRows: 2, TableCols: 2, TableColSpan: 1, TableRowSpan: 1}
	}
	fr := Format{Lang: "fr"}
	snap := textSnapshot(
		cell(0, 0, Segment{Text: "Name"}), cell(0, 1, Segment{Text: "Greeting"}),
		cell(1, 0, Segment{Text: "say "}, Segment{Text: "bonjour", Format: fr}),
		cell(1, 1, Segment{Text: "see "}, Segment{Text: "here", Format: Format{Href: "https://example.org", Lang: "fr"}}),
	)
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	want := "Document(Table(TR(TH TH) TR(TD(Span[fr]) TD(Link[fr](OBJR)))))"
	if got := structureShape(t, data); got != want {
		t.Errorf("structure = %s, want %s", got, want)
	}
}

func TestPopplerReadsTheTaggedFile(t *testing.T) {
	info, err1 := exec.LookPath("pdfinfo")
	totext, err2 := exec.LookPath("pdftotext")
	if err1 != nil || err2 != nil {
		t.Log("poppler is not installed here; the structure was checked with pdfua and the reader library")
		return
	}
	snap := textSnapshot(
		Block{Kind: Heading1, Segments: []Segment{{Text: "Title"}}},
		Block{Kind: BulletItem, Segments: []Segment{{Text: "outer"}}},
		Block{Kind: BulletItem, Level: 1, Segments: []Segment{{Text: "inner"}}},
		Block{Kind: Paragraph, Segments: []Segment{{Text: "visit "}, {Text: "here", Format: Format{Href: "https://example.org"}}, {Text: " bonjour", Format: Format{Lang: "fr"}}}},
	)
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "t.pdf")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(info, f).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Tagged:") || !strings.Contains(string(out), "yes") || strings.Contains(string(out), "Error") {
		t.Errorf("pdfinfo: %v\n%s", err, out)
	}
	txt, err := exec.Command(totext, f, "-").CombinedOutput()
	if err != nil || strings.Contains(string(txt), "Error") || !strings.Contains(string(txt), "visit here bonjour") {
		t.Errorf("pdftotext: %v\n%s", err, txt)
	}
}

func TestVeraPDFAcceptsTheTaggedFiles(t *testing.T) {
	bin := os.Getenv("GOJIBERRY_VERAPDF")
	if bin == "" {
		bin, _ = exec.LookPath("verapdf")
	}
	if bin == "" {
		if os.Getenv("GOJIBERRY_REQUIRE_VERAPDF") == "1" {
			t.Fatal("verapdf is required and not installed")
		}
		t.Skip("verapdf is not installed")
	}
	run := func(s, lang string) Segment { return Segment{Text: s, Format: Format{Lang: lang}} }
	cell := func(r, c int, segs ...Segment) Block {
		return Block{Kind: TableCell, Segments: segs, TableRow: r, TableCol: c, TableRows: 2, TableCols: 2, TableColSpan: 1, TableRowSpan: 1}
	}
	docs := map[string]Snapshot{
		"tagged": taggedSnapshot(t),
		"languages": textSnapshot(
			Block{Kind: Paragraph, Segments: []Segment{{Text: "He said "}, run("bonjour", "fr"), {Text: " then "}, run("Tag", "de-CH")}},
			Block{Kind: Paragraph, Segments: []Segment{{Text: "see "}, {Text: "ici", Format: Format{Href: "https://example.org", Lang: "fr"}}}},
		),
		"table": textSnapshot(
			cell(0, 0, Segment{Text: "Name"}), cell(0, 1, Segment{Text: "Greeting"}),
			cell(1, 0, Segment{Text: "say "}, run("bonjour", "fr")),
			cell(1, 1, Segment{Text: "see "}, Segment{Text: "here", Format: Format{Href: "https://example.org", Lang: "fr"}}),
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
		cmd := exec.Command(bin, "--flavour", "ua1", "--format", "text", f)
		cmd.Env = append(os.Environ(), "JAVA_TOOL_OPTIONS=")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains("\n"+string(out), "\nPASS ") {
			t.Errorf("%s: veraPDF: %v\n%s", name, err, out)
		}
	}
}
