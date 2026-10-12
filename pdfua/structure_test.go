package pdfua

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/export"
)

// buildPDF writes a minimal PDF with a correct cross reference table from numbered object bodies
// (objects[0] is object 1). The trailer names object 1 as /Root and, when info > 0, that object as /Info.
func buildPDF(objects []string, info int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offs := make([]int, len(objects))
	for i, o := range objects {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	infoRef := ""
	if info > 0 {
		infoRef = fmt.Sprintf(" /Info %d 0 R", info)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, infoRef, xref)
	return b.Bytes()
}

// goodCatalog is a catalog that passes every document-level rule; its structure tree root is object 2.
const goodCatalog = "<< /Type /Catalog /Pages 3 0 R /MarkInfo << /Marked true >> /Lang (en-US) " +
	"/ViewerPreferences << /DisplayDocTitle true >> /StructTreeRoot 2 0 R >>"

// structPDF builds a file that passes the document-level rules, whose structure tree root is strRoot
// and whose further objects (from number 5 on) are extra.
func structPDF(strRoot string, extra ...string) []byte {
	objs := []string{
		goodCatalog,
		strRoot,
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Title (A title) >>",
	}
	return buildPDF(append(objs, extra...), 4)
}

func ruleCounts(fs []Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Rule]++
	}
	return m
}

func TestStandardTypesPass(t *testing.T) {
	data := structPDF("<< /Type /StructTreeRoot /K [5 0 R 6 0 R] >>",
		"<< /Type /StructElem /S /Document /P 2 0 R /K [7 0 R] >>",
		"<< /Type /StructElem /S /Sect /P 2 0 R >>",
		"<< /Type /StructElem /S /P /K 0 >>")
	got, err := Check(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("findings for a clean file: %v", got)
	}
}

func TestUnknownTypeIsReportedAndRoleMapFixesIt(t *testing.T) {
	elem := "<< /Type /StructElem /S /Callout /P 2 0 R >>"
	got, err := Check(structPDF("<< /Type /StructTreeRoot /K 5 0 R >>", elem))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Rule != "StructureType" || !strings.Contains(got[0].Message, "/Callout") {
		t.Fatalf("want one StructureType finding naming /Callout, got %v", got)
	}
	if want := "StructureType: " + got[0].Message; got[0].String() != want {
		t.Errorf("String() = %q, want %q", got[0].String(), want)
	}

	// Mapped through /RoleMap the same element is acceptable; a map for another name does not help.
	got, err = Check(structPDF("<< /Type /StructTreeRoot /K 5 0 R /RoleMap << /Callout /P >> >>", elem))
	if err != nil || len(got) != 0 {
		t.Fatalf("mapped type: %v, %v", got, err)
	}
	got, err = Check(structPDF("<< /Type /StructTreeRoot /K 5 0 R /RoleMap << /Other /P >> >>", elem))
	if err != nil || len(got) != 1 || got[0].Rule != "StructureType" {
		t.Fatalf("map for another name: %v, %v", got, err)
	}
}

func TestEachUnknownTypeReportedOnce(t *testing.T) {
	got, err := Check(structPDF("<< /Type /StructTreeRoot /K [5 0 R 6 0 R 7 0 R] >>",
		"<< /S /Foo >>", "<< /S /Foo >>", "<< /S /Bar >>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || ruleCounts(got)["StructureType"] != 2 {
		t.Fatalf("want one finding per distinct type (2), got %v", got)
	}
}

func TestEmptyStructureTreeIsReported(t *testing.T) {
	got, err := Check(structPDF("<< /Type /StructTreeRoot >>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Rule != "StructTreeRoot" || !strings.Contains(got[0].Message, "no elements") {
		t.Fatalf("got %v", got)
	}
	// Marked content ids alone are not elements either.
	got, err = Check(structPDF("<< /Type /StructTreeRoot /K [0 1 2] >>"))
	if err != nil || len(got) != 1 || got[0].Rule != "StructTreeRoot" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestMissingPartsAreReportedEach(t *testing.T) {
	const tail = "/StructTreeRoot 2 0 R >>"
	cases := []struct {
		name    string
		catalog string
		info    string
		want    string // the one rule expected
	}{
		{"not marked", "<< /Pages 3 0 R /MarkInfo << /Marked false >> /Lang (en) /ViewerPreferences << /DisplayDocTitle true >> " + tail, "<< /Title (T) >>", "MarkInfo"},
		{"no MarkInfo", "<< /Pages 3 0 R /Lang (en) /ViewerPreferences << /DisplayDocTitle true >> " + tail, "<< /Title (T) >>", "MarkInfo"},
		{"no Lang", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /ViewerPreferences << /DisplayDocTitle true >> " + tail, "<< /Title (T) >>", "Lang"},
		{"empty Lang", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /Lang () /ViewerPreferences << /DisplayDocTitle true >> " + tail, "<< /Title (T) >>", "Lang"},
		{"Lang is a name", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /Lang /en /ViewerPreferences << /DisplayDocTitle true >> " + tail, "<< /Title (T) >>", "Lang"},
		{"no ViewerPreferences", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /Lang (en) " + tail, "<< /Title (T) >>", "DisplayDocTitle"},
		{"DisplayDocTitle false", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /Lang (en) /ViewerPreferences << /DisplayDocTitle false >> " + tail, "<< /Title (T) >>", "DisplayDocTitle"},
		{"no Title", goodCatalog, "<< /Author (me) >>", "Title"},
		{"empty Title", goodCatalog, "<< /Title () >>", "Title"},
		{"no StructTreeRoot", "<< /Pages 3 0 R /MarkInfo << /Marked true >> /Lang (en) /ViewerPreferences << /DisplayDocTitle true >> >>", "<< /Title (T) >>", "StructTreeRoot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildPDF([]string{
				c.catalog,
				"<< /Type /StructTreeRoot /K 5 0 R >>",
				"<< /Type /Pages /Kids [] /Count 0 >>",
				c.info,
				"<< /S /Document >>",
			}, 4)
			got, err := Check(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Rule != c.want {
				t.Fatalf("want exactly one %s finding, got %v", c.want, got)
			}
		})
	}
}

func TestEverythingMissingReportsEverything(t *testing.T) {
	data := buildPDF([]string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [] /Count 0 >>"}, 0)
	got, err := Check(data)
	if err != nil {
		t.Fatal(err)
	}
	counts := ruleCounts(got)
	for _, r := range []string{"MarkInfo", "Lang", "DisplayDocTitle", "Title", "StructTreeRoot"} {
		if counts[r] != 1 {
			t.Errorf("%s findings = %d, want 1 (%v)", r, counts[r], got)
		}
	}
	if len(got) != 5 {
		t.Errorf("got %d findings, want 5: %v", len(got), got)
	}
}

func TestNoCatalogIsAnError(t *testing.T) {
	// A trailer without /Root: the file parses, but there is nothing to check.
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	off := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 2\n0000000000 65535 f \n%010d 00000 n \ntrailer\n<< /Size 2 >>\nstartxref\n%d\n%%%%EOF\n", off, xref)
	got, err := Check(b.Bytes())
	if err == nil || !strings.Contains(err.Error(), "no document catalog") {
		t.Fatalf("got %v, %v", got, err)
	}
	if got != nil {
		t.Errorf("findings returned with an error: %v", got)
	}
}

func TestTaggedExportReportsNothing(t *testing.T) {
	seg := func(s string) []export.Segment { return []export.Segment{{Text: s}} }
	snap := export.Snapshot{
		Title: "Doc", Lang: "en", PageWidth: 794, PageHeight: 1123,
		MarginLeft: 40, MarginTop: 40, MarginRight: 40, MarginBottom: 40,
		Blocks: []export.Block{
			{Kind: export.Heading1, Segments: seg("Title")},
			{Kind: export.Paragraph, Segments: seg("Body text.")},
			{Kind: export.BulletItem, Segments: seg("one")},
		},
	}
	data, err := export.PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Check(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("exported file has findings: %v", got)
	}
}

func nestedTree(depth int) []byte {
	return structPDF("<< /Type /StructTreeRoot /K " + strings.Repeat("[", depth) + "<< /S /Buried >>" + strings.Repeat("]", depth) + " >>")
}

func TestHostileDeepTreeStaysBounded(t *testing.T) {
	// Arrays nested far deeper than the walk's limit, with an element of an unknown type at the bottom.
	// The walk gives up at its depth limit, so it returns and never sees the buried element.
	done := make(chan []Finding, 1)
	go func() {
		got, _ := Check(nestedTree(5000))
		done <- got
	}()
	select {
	case got := <-done:
		counts := ruleCounts(got)
		if counts["StructureType"] != 0 {
			t.Errorf("walk went past its depth limit: %v", got)
		}
		if counts["StructTreeRoot"] != 1 {
			t.Errorf("want the 'no elements' finding for an unreachable tree, got %v", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Check did not return on a deeply nested tree")
	}
}

func TestDepthLimitBoundary(t *testing.T) {
	if got, _ := Check(nestedTree(100)); ruleCounts(got)["StructureType"] != 1 {
		t.Errorf("element at depth 100 not seen: %v", got)
	}
	if got, _ := Check(nestedTree(300)); ruleCounts(got)["StructureType"] != 0 {
		t.Errorf("element at depth 300 was seen: %v", got)
	}
}

func TestSelfReferencingTreeTerminates(t *testing.T) {
	// Object 5 is its own kid; the depth limit ends the walk.
	got, err := Check(structPDF("<< /Type /StructTreeRoot /K 5 0 R >>", "<< /S /Document /K 5 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestWideTreeStopsAtNodeLimit(t *testing.T) {
	// More elements than maxNodes, then one of an unknown type: the walk stops before reaching it.
	var kids strings.Builder
	kids.WriteString("[")
	for i := 0; i < maxNodes/100+10; i++ {
		kids.WriteString("<< /S /Sect /K [")
		for j := 0; j < 100; j++ {
			kids.WriteString("<< /S /P >> ")
		}
		kids.WriteString("] >> ")
	}
	kids.WriteString("<< /S /Late >> ]")
	got, err := Check(structPDF("<< /Type /StructTreeRoot /K " + kids.String() + " >>"))
	if err != nil {
		t.Fatal(err)
	}
	if n := ruleCounts(got)["StructureType"]; n != 0 {
		t.Fatalf("element after the node limit was examined: %v", got)
	}
}
