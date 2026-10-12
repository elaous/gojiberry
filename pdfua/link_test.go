package pdfua

import (
	"strings"
	"testing"
)

// linkPDF builds a one-page file with the given annotations (object 6 on) and structure elements: the
// structure root's children are the numbered objects named in kids.
func linkPDF(annots string, kids string, extra ...string) []byte {
	objs := []string{
		goodCatalog,
		"<< /Type /StructTreeRoot /K [" + kids + "] >>",
		"<< /Type /Pages /Kids [5 0 R] /Count 1 >>",
		"<< /Title (A title) >>",
		"<< /Type /Page /Parent 3 0 R /MediaBox [0 0 100 100] /Annots [" + annots + "] >>",
	}
	return buildPDF(append(objs, extra...), 4)
}

func findingsOf(t *testing.T, data []byte) []Finding {
	t.Helper()
	got, err := Check(data)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

const linkAnnot = "<< /Type /Annot /Subtype /Link /Rect [10 20 50 40] /StructParent 1 /Border [0 0 0] >>"

func TestLinkAnnotationReferencedByAnOBJRPasses(t *testing.T) {
	data := linkPDF("6 0 R", "7 0 R", linkAnnot,
		"<< /Type /StructElem /S /Link /K [ << /Type /OBJR /Obj 6 0 R /Pg 5 0 R >> ] >>")
	if got := findingsOf(t, data); len(got) != 0 {
		t.Fatalf("findings for a tagged link: %v", got)
	}
}

func TestLinkAnnotationOutsideTheTreeIsReported(t *testing.T) {
	// No /StructParent at all.
	data := linkPDF("6 0 R", "7 0 R", "<< /Type /Annot /Subtype /Link /Rect [10 20 50 40] >>",
		"<< /Type /StructElem /S /Link >>")
	got := findingsOf(t, data)
	if len(got) != 1 || got[0].Rule != "LinkAnnotation" || !strings.Contains(got[0].Message, "no /StructParent") {
		t.Fatalf("got %v", got)
	}

	// A key, but no /OBJR in the tree uses it.
	data = linkPDF("6 0 R", "7 0 R", linkAnnot, "<< /Type /StructElem /S /Link >>")
	got = findingsOf(t, data)
	if len(got) != 1 || got[0].Rule != "LinkAnnotation" || !strings.Contains(got[0].Message, "/OBJR") {
		t.Fatalf("got %v", got)
	}

	// An /OBJR for another annotation (other key) does not cover this one.
	data = linkPDF("6 0 R 8 0 R", "7 0 R", linkAnnot,
		"<< /Type /StructElem /S /Link /K [ << /Type /OBJR /Obj 8 0 R >> ] >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 20 90 40] /StructParent 2 >>")
	if got := findingsOf(t, data); len(got) != 1 || got[0].Rule != "LinkAnnotation" {
		t.Fatalf("want one finding for the uncovered link, got %v", got)
	}

	// The same key on a different rectangle is not the same annotation.
	data = linkPDF("6 0 R", "7 0 R", linkAnnot,
		"<< /Type /StructElem /S /Link /K [ << /Type /OBJR /Obj 8 0 R >> ] >>",
		"<< /Type /Annot /Subtype /Link /Rect [1 2 3 4] /StructParent 1 >>")
	if got := findingsOf(t, data); len(got) != 1 || got[0].Rule != "LinkAnnotation" {
		t.Fatalf("want one finding for a rectangle that differs, got %v", got)
	}
}

func TestOtherAnnotationsNeedNoTreeEntry(t *testing.T) {
	data := linkPDF("6 0 R", "7 0 R", "<< /Type /Annot /Subtype /Text /Rect [1 2 3 4] >>", "<< /Type /StructElem /S /P >>")
	if got := findingsOf(t, data); len(got) != 0 {
		t.Fatalf("findings for a note annotation: %v", got)
	}
}

func TestElementLanguageMustBeAValidTag(t *testing.T) {
	ok := linkPDF("", "6 0 R", "<< /Type /StructElem /S /Span /Lang (fr-CA) >>")
	if got := findingsOf(t, ok); len(got) != 0 {
		t.Fatalf("a valid element language was reported: %v", got)
	}
	bad := linkPDF("", "6 0 R", "<< /Type /StructElem /S /Span /Lang (not a tag!) >>")
	got := findingsOf(t, bad)
	if len(got) != 1 || got[0].Rule != "Lang" || !strings.Contains(got[0].Message, "not a tag!") {
		t.Fatalf("got %v", got)
	}
}

func TestCatalogLanguageMustBeAValidTag(t *testing.T) {
	objs := []string{
		strings.Replace(goodCatalog, "(en-US)", "(english please)", 1),
		"<< /Type /StructTreeRoot /K [5 0 R] >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Title (A title) >>",
		"<< /Type /StructElem /S /P >>",
	}
	got := findingsOf(t, buildPDF(objs, 4))
	if len(got) != 1 || got[0].Rule != "Lang" || !strings.Contains(got[0].Message, "catalog") {
		t.Fatalf("got %v", got)
	}
}
