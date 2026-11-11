package officecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/odtimport"
)

func TestOdtVerticalTextRoundTrip(t *testing.T) {
	snap := export.Snapshot{Title: "T", Vertical: true, PageWidth: 794, PageHeight: 1123, MarginLeft: 96, MarginTop: 96, MarginRight: 96, MarginBottom: 96,
		Blocks: []export.Block{{Kind: export.Paragraph, Segments: []export.Segment{{Text: "日本語"}}}}}
	data, err := export.ODT{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	d, err := odtimport.Import(data)
	if err != nil || !d.PageLayout().Vertical {
		t.Errorf("vertical after import: %v, %v", d.PageLayout().Vertical, err)
	}
	snap.Vertical = false
	data, _ = export.ODT{}.Export(snap)
	if d, _ := odtimport.Import(data); d.PageLayout().Vertical {
		t.Error("horizontal text came back vertical")
	}
}

func TestLibreOfficeOpensVerticalText(t *testing.T) {
	bin := soffice(t)
	snap := export.Snapshot{Title: "T", Vertical: true, PageWidth: 794, PageHeight: 1123, MarginLeft: 96, MarginTop: 96, MarginRight: 96, MarginBottom: 96,
		Blocks: []export.Block{{Kind: export.Paragraph, Segments: []export.Segment{{Text: "日本語のテキスト"}}}}}
	for ext, ex := range map[string]export.Exporter{"docx": export.DOCX{}, "odt": export.ODT{}} {
		data, err := ex.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "v."+ext)
		if err := os.WriteFile(f, data, 0o600); err != nil {
			t.Fatal(err)
		}
		out := convert(t, bin, "", "txt:Text (encoded):UTF8", f)
		txt, err := os.ReadFile(filepath.Join(out, "v.txt"))
		if err != nil || !strings.Contains(string(txt), "日本語のテキスト") {
			t.Errorf("%s: %v %q", ext, err, txt)
		}
	}
}
