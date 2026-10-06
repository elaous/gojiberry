package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// StatusError is the error HTTPAdapter returns for a response the server answered with an
// unexpected status; test with errors.As to act on Status (401 and 403 mean the credentials
// were refused).
type StatusError struct {
	Status int    // the HTTP status code
	Text   string // the status line, "403 Forbidden"
	Body   string // the start of the response body
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("docstore: server said %s: %s", e.Text, e.Body)
}

func httpError(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &StatusError{Status: resp.StatusCode, Text: resp.Status, Body: strings.TrimSpace(string(msg))}
}

// RedactURL returns raw for a log line: the user name and password, the query and the fragment
// (where a deployment may put a token) are removed. Text that is not a URL is returned as
// "<invalid URL>".
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" && u.Host == "" {
		return "<invalid URL>"
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	return u.String()
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

// SaveIf implements ConditionalAdapter with If-Match / If-None-Match on the PUT: the
// server answers 412 when the document changed and 501 when its store cannot say.
func (a *HTTPAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if data == nil {
		data = []byte{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.base+docPath(id), bytes.NewReader(data))
	if err != nil {
		return err
	}
	for k, vs := range a.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if prev == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", `"`+prev+`"`)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return ErrConflict
	case resp.StatusCode == http.StatusNotImplemented:
		return ErrConditionalUnsupported
	case resp.StatusCode/100 != 2:
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

type authKey struct{}

// WithAuthorization returns a context whose Authorization header value HTTPAdapter.Renew sends,
// so a renewal is made with the credentials of the person whose edit caused it (the server
// checks the same write permission as for a save).
func WithAuthorization(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, authKey{}, value)
}

var _ Renewer = (*HTTPAdapter)(nil)

// Renew implements Renewer with POST /docs/{id}/renew: the server moves the document's
// deadline if the caller may write it (see WithAuthorization) and the document is alive.
// ErrNotFound means it does not exist or has expired.
func (a *HTTPAdapter) Renew(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+docPath(id)+"/renew", nil)
	if err != nil {
		return err
	}
	for k, vs := range a.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if v, _ := ctx.Value(authKey{}).(string); v != "" {
		req.Header.Set("Authorization", v)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// The error carries the request URL, which may hold credentials: it ends up in the
		// hub's log.
		var ue *url.Error
		if errors.As(err, &ue) {
			ue.URL = RedactURL(ue.URL)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode/100 != 2:
		return httpError(resp)
	}
	return nil
}
