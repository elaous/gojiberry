package serve

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/export/remote"
	"github.com/elaous/gojiberry/transfer"
)

func sample() docfile.Document {
	return docfile.Document{Title: "Report", Content: docfile.FromItems([]canvas.Item{
		canvas.Chars{Text: "Heading\nBody with words.\n"},
	}), Footnotes: []docfile.Footnote{{ID: "1", Text: "a note"}}}
}

// The formats the light side offers are those the heavy side registers, in the same order.
func TestTheFormatTablesAreWhatExportRegisters(t *testing.T) {
	direct := transfer.Default()
	export.Register(direct)
	var exps, imps []transfer.Format
	for _, e := range direct.Exporters() {
		exps = append(exps, e.Format())
	}
	for _, i := range direct.Importers() {
		imps = append(imps, i.Format())
	}
	via := transfer.Default()
	remote.Register(via, Handle)
	var vexps, vimps []transfer.Format
	for _, e := range via.Exporters() {
		vexps = append(vexps, e.Format())
	}
	for _, i := range via.Importers() {
		vimps = append(vimps, i.Format())
	}
	if !reflect.DeepEqual(exps, vexps) {
		t.Errorf("exporters differ:\n%v\n%v", exps, vexps)
	}
	if !reflect.DeepEqual(imps, vimps) {
		t.Errorf("importers differ:\n%v\n%v", imps, vimps)
	}
}

// Through the protocol every format gives what the direct call gives.
func TestEveryFormatThroughTheProtocolMatchesTheDirectCall(t *testing.T) {
	direct := transfer.Default()
	export.Register(direct)
	via := transfer.Default()
	remote.Register(via, Handle)
	doc := sample()
	for _, e := range remote.Exporters {
		_, want, _, err := direct.Export(e.Name, doc)
		if err != nil {
			t.Fatal(err)
		}
		file, got, mime, err := via.Export(e.Name, doc)
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if mime != e.MIME || !strings.HasSuffix(file, e.Exts[0]) {
			t.Errorf("%s: %s %s", e.Name, file, mime)
		}
		if e.Name == "pdf" { // a PDF may carry a time
			if len(got) == 0 || !bytes.HasPrefix(got, []byte("%PDF")) {
				t.Errorf("pdf: %q", got[:min(8, len(got))])
			}
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes through the protocol, %d direct", e.Name, len(got), len(want))
		}
	}
	// Import: a file we wrote reads back the same either way.
	for _, f := range []string{"docx", "html", "odt"} {
		_, data, _, _ := via.Export(f, doc)
		want, err := direct.Import(f, "", data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := via.Import(f, "", data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		a, _ := docfile.JSON.Marshal(want)
		b, _ := docfile.JSON.Marshal(got)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: imported documents differ", f)
		}
	}
	if _, err := via.Import("docx", "", []byte("not a zip")); err == nil {
		t.Error("an error crosses the protocol")
	}
	if _, err := Handle("nope", "docx", nil); err != ErrUnknownOp {
		t.Errorf("unknown op: %v", err)
	}
}

func TestReportsThroughTheProtocolMatchTheDirectCall(t *testing.T) {
	doc := sample()
	for _, f := range export.Formats {
		got, err := remote.Fidelity(Handle, f, doc)
		if err != nil {
			t.Fatal(err)
		}
		if want := export.Fidelity(f, doc); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v vs %+v", f, got, want)
		}
	}
	page := []byte(`<p>x</p><script>1</script>`)
	got, err := remote.ImportFidelity(Handle, "html", page)
	if err != nil || !reflect.DeepEqual(got, export.ImportFidelity("html", page)) || !got.Known {
		t.Errorf("import report: %+v %v", got, err)
	}
	if imp := remote.WordImport(Handle); imp == nil {
		t.Error("WordImport")
	}
}
