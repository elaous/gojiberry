package export

import "testing"

func TestBRFExportReturnsBrailleVerbatim(t *testing.T) {
	b := BRF{Braille: ",hello world"}
	data, err := b.Export(Snapshot{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if string(data) != ",hello world" {
		t.Fatalf("Export = %q, want the Braille field verbatim", data)
	}
}

func TestBRFExportIgnoresSnapshotContent(t *testing.T) {
	b := BRF{Braille: "fixed output"}
	data, err := b.Export(Snapshot{Blocks: []Block{{Kind: Paragraph, Segments: []Segment{{Text: "ignored"}}}}})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if string(data) != "fixed output" {
		t.Fatalf("Export = %q, want the Braille field regardless of snap's own content", data)
	}
}

func TestBRFExportEmptyBrailleReturnsEmptyBytes(t *testing.T) {
	b := BRF{}
	data, err := b.Export(Snapshot{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("Export = %q, want empty", data)
	}
}
