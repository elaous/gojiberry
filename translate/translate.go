package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a translate server.
type Client struct {
	// BaseURL is where the server is ("https://host" or "/translate-api").
	BaseURL string
	Header  http.Header
	HTTP    *http.Client
}

// Result is a translation.
type Result struct {
	Text     string
	Detected string
}

// Translator turns text into another language.
type Translator interface {
	Translate(ctx context.Context, text, target, source string) (Result, error)
}

var _ Translator = Client{}

func (c Client) Translate(ctx context.Context, text, target, source string) (Result, error) {
	if strings.TrimSpace(text) == "" {
		return Result{}, nil
	}
	body, err := json.Marshal(map[string]string{"text": text, "targetLang": target, "sourceLang": source})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.BaseURL, "/")+"/translate", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	for k, v := range c.Header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("translate: the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		TranslatedText         string `json:"translatedText"`
		DetectedSourceLanguage string `json:"detectedSourceLanguage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return Result{}, err
	}
	return Result{Text: out.TranslatedText, Detected: out.DetectedSourceLanguage}, nil
}
