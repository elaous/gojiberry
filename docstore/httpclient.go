package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPAdapter is an Adapter that talks to a docstore server.
type HTTPAdapter struct {
	base   string
	client *http.Client
	// Header is added to every request.
	Header http.Header

	renewWarn sync.Once // the first unsupported-renew answer is logged
}

// NewHTTPAdapter returns an adapter for the server at base.
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

// StatusError is the error HTTPAdapter returns for an HTTP status.
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

// RedactURL returns raw for a log line, without credentials.
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

// SaveIf implements ConditionalAdapter over HTTP.
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

// Load implements Adapter.
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

// WithAuthorization returns a context with the authorization header.
func WithAuthorization(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, authKey{}, value)
}

var _ Renewer = (*HTTPAdapter)(nil)

// Renew implements Renewer with POST /docs/{id}/renew.
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
		var ue *url.Error
		if errors.As(err, &ue) {
			ue.URL = RedactURL(ue.URL)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound && isGoneAnswer(resp):
		return ErrNotFound
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented:
		se := httpError(resp)
		a.renewWarn.Do(func() {
			log.Printf("docstore: the renew route answered %d without the document-gone marker (%s): renewals are not working, which is not taken as the document being gone. Check that the base URL %s points at a docstore server that has POST /docs/{id}/renew and expires documents (GOJIBERRY_DOCSTORE_EXPIRY_TTL), and that no proxy answers 404 for it; documents will expire under active editing until it does. Further answers are counted, not logged.", resp.StatusCode, strings.TrimSpace(se.(*StatusError).Body), RedactURL(a.base))
		})
		return fmt.Errorf("%w: %w", ErrRenewUnsupported, se)
	case resp.StatusCode/100 != 2:
		return httpError(resp)
	}
	return nil
}

const GoneHeader = "X-Gojiberry-Gone"

// ErrRenewUnsupported is returned by servers that lack renew.
var ErrRenewUnsupported = errors.New("docstore: the server does not support renewals here")

func isGoneAnswer(resp *http.Response) bool {
	if resp.Header.Get(GoneHeader) != "1" {
		return false
	}
	var body struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return json.Unmarshal(b, &body) == nil && body.Error == "gone"
}
