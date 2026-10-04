// Package grammar checks grammar with a language model running behind an
// Ollama-compatible server (a local one needs no API key). It finds the mistakes
// a fixed spelling list cannot: agreement, tense, wrong word forms.
//
// The model's own arithmetic on character positions is unreliable, so every issue
// is checked against the text: one whose range does not exist is dropped, and
// one whose range holds something other than what the model said is moved to
// where the text really is, or dropped.
package grammar

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

// Issue is a grammar problem: the range [From, To) (rune offsets) of the text,
// what is wrong and what to write instead.
type Issue struct {
	From, To   int
	Message    string
	Suggestion string
}

// Client asks a model.
type Client struct {
	// BaseURL is the Ollama server ("http://localhost:11434").
	BaseURL string
	// Model is the model to ask ("llama3.1").
	Model string
	// HTTP is the client to use; nil means one with a 90 second timeout (a model
	// can take a while).
	HTTP *http.Client
}

const prompt = `You are a grammar checker. Find grammar mistakes in the text below -- subject-verb agreement, pronoun agreement, tense consistency, and similar context-dependent errors a fixed pattern rule can't catch. Do not flag spelling or punctuation.

Respond with ONLY a JSON array, no other text, in this exact shape: [{"offset": <int>, "length": <int>, "message": "<short description>", "suggestion": "<replacement text>"}]. offset/length are 0-indexed character positions into the text below. Return [] if there are no issues.

Text:
%s`

// MaxText is the longest text one call checks, in runes (longer text is cut).
const MaxText = 8000

// Check asks the model for the grammar issues in text.
func (c Client) Check(ctx context.Context, text string) ([]Issue, error) {
	rs := []rune(text)
	if len(rs) > MaxText {
		rs = rs[:MaxText]
		text = string(rs)
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	if c.Model == "" {
		return nil, errors.New("grammar: no model given")
	}
	body, err := json.Marshal(map[string]any{"model": c.Model, "prompt": fmt.Sprintf(prompt, text), "format": "json", "stream": false})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.BaseURL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grammar: the model server answered %d", resp.StatusCode)
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return ParseIssues(out.Response, text)
}

type rawIssue struct {
	Offset     int    `json:"offset"`
	Length     int    `json:"length"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
	// Text is what the model says is at that place, if it says (some models do).
	Text string `json:"text"`
}

// ParseIssues finds the JSON array in a model's answer (which may come wrapped
// in commentary or a code fence) and turns it into issues for text. An entry
// whose range is outside the text is dropped.
func ParseIssues(answer, text string) ([]Issue, error) {
	start, end := strings.IndexByte(answer, '['), strings.LastIndexByte(answer, ']')
	if start < 0 || end < start {
		return nil, errors.New("grammar: no JSON array in the model's answer")
	}
	var raw []rawIssue
	if err := json.Unmarshal([]byte(answer[start:end+1]), &raw); err != nil {
		return nil, err
	}
	rs := []rune(text)
	out := make([]Issue, 0, len(raw))
	for _, r := range raw {
		from, to := r.Offset, r.Offset+r.Length
		if from < 0 || to <= from || to > len(rs) {
			continue
		}
		// If the model quoted the words, trust the words over the numbers.
		if r.Text != "" && string(rs[from:to]) != r.Text {
			if i := strings.Index(text, r.Text); i >= 0 {
				from = len([]rune(text[:i]))
				to = from + len([]rune(r.Text))
			} else {
				continue
			}
		}
		out = append(out, Issue{From: from, To: to, Message: r.Message, Suggestion: r.Suggestion})
	}
	return out, nil
}
