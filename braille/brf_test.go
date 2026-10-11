package braille

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
)

func TestBRFExportReturnsBrailleVerbatim(t *testing.T) {
	b := BRF{Braille: ",hello world"}
	data, err := b.Export(export.Snapshot{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if string(data) != ",hello world" {
		t.Fatalf("Export = %q, want the Braille field verbatim", data)
	}
}

func TestBRFExportIgnoresSnapshotContent(t *testing.T) {
	b := BRF{Braille: "fixed output"}
	data, err := b.Export(export.Snapshot{Blocks: []export.Block{{Kind: export.Paragraph, Segments: []export.Segment{{Text: "ignored"}}}}})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if string(data) != "fixed output" {
		t.Fatalf("Export = %q, want the Braille field regardless of snap's own content", data)
	}
}

func TestBRFExportEmptyBrailleReturnsEmptyBytes(t *testing.T) {
	b := BRF{}
	data, err := b.Export(export.Snapshot{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("Export = %q, want empty", data)
	}
}

func TestBrailleAsksTheServer(t *testing.T) {
	doc := docfile.Document{Title: "Report", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "Intro\n"}}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text, Table string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/translate" || !strings.Contains(req.Text, "Intro") || req.Table != "en-us-g1.ctb" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"braille": ",intro"})
	}))
	defer srv.Close()
	b := Braille{Backend: srv.URL + "/", Table: "en-us-g1.ctb"}
	out, err := b.Export(doc)
	if err != nil || string(out) != ",intro" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (Braille{Backend: srv.URL, Table: "other"}).Export(doc); err == nil {
		t.Error("a refusal from the server must be an error")
	}
	if got := b.Format(); got.Name != "brf" || got.Exts[0] != ".brf" {
		t.Errorf("format %+v", got)
	}
}
