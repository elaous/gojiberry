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

type Issue struct {
	From, To   int
	Message    string
	Suggestion string
}

// Checker finds grammar issues in a text.
type Checker interface {
	Check(ctx context.Context, text string) ([]Issue, error)
}

var _ Checker = Client{}

func Prompt(text string) string { return fmt.Sprintf(prompt, text) }

func Cut(text string) string {
	if rs := []rune(text); len(rs) > MaxText {
		return string(rs[:MaxText])
	}
	return text
}

// Client asks a model.
type Client struct {
	// BaseURL is the Ollama server ("http://localhost:11434").
	BaseURL string
	// Model is the model to ask ("llama3.1").
	Model string
	HTTP  *http.Client
}

const prompt = `You are a grammar checker. Find grammar mistakes in the text below -- subject-verb agreement, pronoun agreement, tense consistency, and similar context-dependent errors a fixed pattern rule can't catch. Do not flag spelling or punctuation.

Respond with ONLY a JSON array, no other text, in this exact shape: [{"offset": <int>, "length": <int>, "message": "<short description>", "suggestion": "<replacement text>"}]. offset/length are 0-indexed character positions into the text below. Return [] if there are no issues.

Text:
%s`

const MaxText = 8000

// Check asks the model for the grammar issues in text.
func (c Client) Check(ctx context.Context, text string) ([]Issue, error) {
	text = Cut(text)
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
	Text       string `json:"text"`
}

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
