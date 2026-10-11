// Package braille writes Braille Ready Format (.brf, ASCII braille). Transcription needs liblouis, a C library
// with no Go binding in this stack, so the exporter sends the text to a running braille server (braille/server,
// cmd/brailleserver) and the BRF type holds what it answered. The server is in braille/server.
package braille

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

// Braille writes Braille Ready Format (.brf, ASCII braille). Transcription
// needs liblouis, so the text is sent to a running cmd/brailleserver at
// Backend; Table overrides the server's own table (for example "en-us-g1.ctb"
// for uncontracted braille). Client may be nil.
type Braille struct {
	Backend string
	Table   string
	Client  *http.Client
}

// Format implements transfer.Exporter.
func (Braille) Format() transfer.Format {
	return transfer.Format{Name: "brf", Label: "Braille (BRF)", Exts: []string{".brf"}, MIME: "text/plain"}
}

// Export implements transfer.Exporter.
func (b Braille) Export(d docfile.Document) ([]byte, error) {
	text, err := export.PlainText{}.Export(export.FromDocument(d))
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"text": string(text), "table": b.Table})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(b.Backend, "/")+"/translate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := b.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("braille: the braille server answered %d", resp.StatusCode)
	}
	var out struct{ Braille string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return BRF{Braille: out.Braille}.Export(export.Snapshot{})
}
