package export

import (
	"strings"
	"testing"
)

func TestVerticalTextIsWrittenWhereTheFormatHasIt(t *testing.T) {
	snap := Snapshot{Title: "T", Vertical: true, PageWidth: 794, PageHeight: 1123, Blocks: []Block{{Kind: Paragraph, Segments: []Segment{{Text: "日本語"}}}}}
	h, err := HTML{}.Export(snap)
	if err != nil || !strings.Contains(string(h), `<html style="writing-mode:vertical-rl">`) {
		t.Errorf("HTML: %v\n%.200s", err, h)
	}
	e, err := EPUB{}.Export(snap)
	if err != nil || !strings.Contains(zipText(t, e, "OEBPS/content.xhtml"), `style="writing-mode:vertical-rl"`) {
		t.Errorf("EPUB: %v", err)
	}
	d, err := DOCX{}.Export(snap)
	if err != nil || !strings.Contains(zipText(t, d, "word/document.xml"), `<w:textDirection w:val="tbRl"/>`) {
		t.Errorf("DOCX: %v", err)
	}
	o, err := ODT{}.Export(snap)
	if err != nil || !strings.Contains(zipText(t, o, "styles.xml"), `style:writing-mode="tb-rl"`) {
		t.Errorf("ODT: %v", err)
	}
	snap.Vertical = false
	h, _ = HTML{}.Export(snap)
	if strings.Contains(string(h), "writing-mode") {
		t.Error("horizontal text says a writing mode")
	}
}
