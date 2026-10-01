package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPAdapter is an Adapter that talks to a docstore Server over HTTP.
// It is how code that cannot reach a database itself (a WASM app in a
// browser) uses whichever backend the server was configured with --
// Postgres, SQLite, S3 and so on.
type HTTPAdapter struct {
	base   string
	client *http.Client
	// Header is added to every request (for example Authorization).
	Header http.Header
}

// NewHTTPAdapter returns an adapter for the server at baseURL
// ("https://host" or "/api" for the same origin). A nil client means
// http.DefaultClient.
func NewHTTPAdapter(baseURL string, client *http.Client) *HTTPAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPAdapter{base: strings.TrimRight(baseURL, "/"), client: client, Header: http.Header{}}
}

func (a *HTTPAdapter) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return nil, err
	}
	for k, vs := range a.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return a.client.Do(req)
}

func httpError(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("docstore: server said %s: %s", resp.Status, strings.TrimSpace(string(msg)))
}

func docPath(id string) string { return "/docs/" + url.PathEscape(id) }

// Save implements Adapter.
func (a *HTTPAdapter) Save(ctx context.Context, id string, data []byte) error {
	if data == nil {
		data = []byte{}
	}
	resp, err := a.do(ctx, http.MethodPut, docPath(id), data)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return httpError(resp)
	}
	return nil
}

// Load implements Adapter. The server does not send a modification
// time with a document, so UpdatedAt is the time of the load; List has
// the real one.
func (a *HTTPAdapter) Load(ctx context.Context, id string) (Document, error) {
	resp, err := a.do(ctx, http.MethodGet, docPath(id), nil)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return Document{}, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return Document{}, httpError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: id, Data: data, UpdatedAt: time.Now()}, nil
}

// List implements Adapter.
func (a *HTTPAdapter) List(ctx context.Context) ([]Meta, error) {
	resp, err := a.do(ctx, http.MethodGet, "/docs", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, httpError(resp)
	}
	var metas []Meta
	if err := json.NewDecoder(resp.Body).Decode(&metas); err != nil {
		return nil, err
	}
	return metas, nil
}

// Delete implements Adapter.
func (a *HTTPAdapter) Delete(ctx context.Context, id string) error {
	resp, err := a.do(ctx, http.MethodDelete, docPath(id), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return httpError(resp)
	}
	return nil
}
