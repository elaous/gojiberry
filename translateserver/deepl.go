package translateserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Translation is one translated text.
type Translation struct {
	Text                   string
	DetectedSourceLanguage string
}

// deeplClient is the one DeepL call this server makes (POST /v2/translate), written against
// the documented HTTP API instead of a client library: the call is a JSON body and a header.
type deeplClient struct {
	key  string
	base string
	http *http.Client
}

const (
	deeplFreeBase = "https://api-free.deepl.com"
	deeplProBase  = "https://api.deepl.com"
	// maxDeeplReply bounds what is read back (a request is at most 128 KiB of text).
	maxDeeplReply = 1 << 20
)

// newDeeplClient makes a client for an API key. Free-plan keys end in ":fx" and use the free
// endpoint; every other key uses the pro one.
func newDeeplClient(key string) (*deeplClient, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("translateserver: empty DeepL API key")
	}
	base := deeplProBase
	if strings.HasSuffix(key, ":fx") {
		base = deeplFreeBase
	}
	return &deeplClient{key: key, base: base, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Translate translates texts into target (e.g. "FR"); source may be "" to let DeepL detect it.
func (c *deeplClient) Translate(ctx context.Context, texts []string, target, source string) ([]Translation, error) {
	body := struct {
		Text       []string `json:"text"`
		TargetLang string   `json:"target_lang"`
		SourceLang string   `json:"source_lang,omitempty"`
	}{texts, target, source}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v2/translate", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDeeplReply))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return nil, fmt.Errorf("deepl: %s: %s", resp.Status, msg)
	}
	var out struct {
		Translations []struct {
			Text     string `json:"text"`
			Detected string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("deepl: unreadable reply: %w", err)
	}
	res := make([]Translation, len(out.Translations))
	for i, t := range out.Translations {
		res[i] = Translation{Text: t.Text, DetectedSourceLanguage: t.Detected}
	}
	return res, nil
}
