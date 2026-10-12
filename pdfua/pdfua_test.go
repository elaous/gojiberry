package pdfua

import (
	"bytes"
	"testing"

	"github.com/signintech/gopdf"
)

func TestGarbageIsAnError(t *testing.T) {
	if _, err := Check([]byte("not a pdf")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestUntaggedFileIsReported(t *testing.T) {
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.AddPage()
	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Check(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, f := range got {
		rules[f.Rule] = true
	}
	for _, want := range []string{"MarkInfo", "Lang", "StructTreeRoot", "DisplayDocTitle", "Title"} {
		if !rules[want] {
			t.Errorf("no %s finding in %v", want, got)
		}
	}
}
