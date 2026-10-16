// Package client is the browser's (or any program's) side of the public offer served by docstore/public: an
// anonymous session kept as a token, the one document that session may have, the edit and view links, the
// countdown to its deletion, and the report route. It holds no state beyond the token; where the token is kept
// between visits is the caller's (the showcase keeps it in localStorage).
//
//	c := &client.Client{BaseURL: "https://host"}
//	_ = c.NewSession(ctx)               // or c.Token = the one kept
//	doc, _ := c.Create(ctx, bytes)      // first save; later c.Save(ctx, bytes) renews the 24 hours
//	edit := client.EditLink(page, c.Token)   // anyone with it can edit: it is the session token
//	view := client.ViewLink(page, doc.ViewID) // read only, cannot renew
//	left := client.Remaining(doc.Expires, time.Now())
//
// A link is a capability: the edit link carries the session token in the URL fragment (which a browser does not
// send to any server, so it reaches the page and nothing else), the view link carries the view ID.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Errors a call may return (wrapped in *Error with the status and the wait). Test them with errors.Is.
var (
	ErrNoSession   = errors.New("client: no session; call NewSession or set Token")
	ErrNotFound    = errors.New("client: not found or expired")                       // 404
	ErrGone        = errors.New("client: taken down")                                 // 410
	ErrExists      = errors.New("client: the session has a document already")         // 409
	ErrForbidden   = errors.New("client: refused")                                    // 401, 403
	ErrTooLarge    = errors.New("client: the document is over the size limit")        // 413
	ErrRateLimited = errors.New("client: too many requests or over the daily budget") // 429
	ErrBusy        = errors.New("client: the offer is busy or full, try later")       // 503
	ErrRefused     = errors.New("client: the content was refused")                    // 422
)

// Error is a refusal by the server.
type Error struct {
	Status int
	// RetryAfter is how long the server asked to wait (429, 503); zero if it did not say.
	RetryAfter time.Duration
	Message    string
	kind       error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("public offer: %d", e.Status)
	if e.Message != "" {
		s += " " + e.Message
	}
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (try again in %s)", e.RetryAfter.Round(time.Second))
	}
	return s
}

// Unwrap lets errors.Is match the sentinel for the status.
func (e *Error) Unwrap() error { return e.kind }

// Client talks to a public offer. Not safe for concurrent use of Token; the calls themselves are.
type Client struct {
	// BaseURL is the server ("https://host"); "" is the page's own origin in a browser.
	BaseURL string
	// HTTP is the client to use (http.DefaultClient when nil).
	HTTP *http.Client
	// Token is the session token (the edit capability); NewSession sets it.
	Token string
	// Terms is the link to the terms the server sent with its last answer (Link: <url>; rel="terms"), "" if none.
	Terms string
}

// Doc is the session's document as the server describes it.
type Doc struct {
	// ID is the document's ID (empty from Download: the edit link does not need it).
	ID     string `json:"id"`
	ViewID string `json:"viewId"`
	// Expires is when it is deleted unless edited again.
	Expires  time.Time `json:"expires"`
	Versions int       `json:"versions"`
}

func (c *Client) do(ctx context.Context, method, path string, auth bool, body []byte, contentType string) (*http.Response, []byte, error) {
	if auth && c.Token == "" {
		return nil, nil, ErrNoSession
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+path, rd)
	if err != nil {
		return nil, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, nil, err
	}
	c.noteTerms(resp)
	if resp.StatusCode/100 != 2 {
		return resp, data, refusal(resp, data)
	}
	return resp, data, nil
}

// noteTerms keeps the terms link of an answer.
func (c *Client) noteTerms(resp *http.Response) {
	for _, l := range resp.Header.Values("Link") {
		for _, part := range strings.Split(l, ",") {
			part = strings.TrimSpace(part)
			if !strings.Contains(part, `rel="terms"`) && !strings.Contains(part, `rel=terms`) {
				continue
			}
			if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i == 0 && j > 1 {
				if u, err := url.Parse(part[1:j]); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
					c.Terms = u.String()
				}
			}
		}
	}
}

func refusal(resp *http.Response, data []byte) error {
	e := &Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(bytes.TrimSpace(data[:min(len(data), 200)])))}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		e.kind = ErrNotFound
	case http.StatusGone:
		e.kind = ErrGone
	case http.StatusConflict:
		e.kind = ErrExists
	case http.StatusUnauthorized, http.StatusForbidden:
		e.kind = ErrForbidden
	case http.StatusRequestEntityTooLarge:
		e.kind = ErrTooLarge
	case http.StatusTooManyRequests:
		e.kind = ErrRateLimited
	case http.StatusServiceUnavailable:
		e.kind = ErrBusy
	case http.StatusUnprocessableEntity:
		e.kind = ErrRefused
	}
	return e
}

// expires reads the deadline an answer carries (X-Gojiberry-Expires).
func expires(resp *http.Response) time.Time {
	t, _ := time.Parse(time.RFC3339, resp.Header.Get("X-Gojiberry-Expires"))
	return t
}

// NewSession asks for an anonymous session and keeps its token.
func (c *Client) NewSession(ctx context.Context) error {
	_, data, err := c.do(ctx, http.MethodPost, "/public/session", false, nil, "")
	if err != nil {
		return err
	}
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil || s.Token == "" {
		return errors.New("client: the server's session answer is not readable")
	}
	c.Token = s.Token
	return nil
}

// Create makes the session's document from content (409 / ErrExists if it has one).
func (c *Client) Create(ctx context.Context, content []byte) (Doc, error) {
	_, data, err := c.do(ctx, http.MethodPost, "/public/doc", true, content, "application/octet-stream")
	if err != nil {
		return Doc{}, err
	}
	var d Doc
	if err := json.Unmarshal(data, &d); err != nil {
		return Doc{}, errors.New("client: the server's document answer is not readable")
	}
	return d, nil
}

// Save stores an edit and returns the new deadline: 24 hours from now. An expired document is ErrNotFound; it is
// not revived.
func (c *Client) Save(ctx context.Context, content []byte) (time.Time, error) {
	resp, _, err := c.do(ctx, http.MethodPut, "/public/doc", true, content, "application/octet-stream")
	if err != nil {
		return time.Time{}, err
	}
	return expires(resp), nil
}

// Download returns the session's document with its deadline and view ID (what a browser that only has the edit link
// needs to show both links); it does not renew the document.
func (c *Client) Download(ctx context.Context) (content []byte, doc Doc, err error) {
	resp, data, err := c.do(ctx, http.MethodGet, "/public/doc", true, nil, "")
	if err != nil {
		return nil, Doc{}, err
	}
	return data, Doc{ViewID: resp.Header.Get("X-Gojiberry-View-Id"), Expires: expires(resp)}, nil
}

// Discard deletes the document, freeing the session to start another.
func (c *Client) Discard(ctx context.Context) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/public/doc", true, nil, "")
	return err
}

// View returns the document a view ID names, and its deadline; it never renews it.
func (c *Client) View(ctx context.Context, viewID string) (content []byte, deadline time.Time, err error) {
	resp, data, err := c.do(ctx, http.MethodGet, "/public/view/"+url.PathEscape(viewID), false, nil, "")
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, expires(resp), nil
}

// Report categories the server knows.
const (
	CategorySpam    = "spam"
	CategoryIllegal = "illegal"
	CategoryCSAM    = "csam"
	CategoryOther   = "other"
)

// Report tells the operator about a document (id is its view ID or its document ID). The server answers 202
// whether or not it kept the report (a duplicate, or no slot), so success here means it was received.
func (c *Client) Report(ctx context.Context, id, category, reason string) error {
	body, _ := json.Marshal(map[string]string{"id": id, "category": category, "reason": reason})
	_, _, err := c.do(ctx, http.MethodPost, "/public/report", false, body, "application/json")
	return err
}

// ---- links ----

// EditLink is the address that opens the session's document for editing: the page with the token in the
// fragment. Whoever has it can edit and renew the document, so it is shown to the person and shared by them.
func EditLink(page, token string) string { return withFragment(page, "edit="+url.QueryEscape(token)) }

// ViewLink is the address that opens the document read only.
func ViewLink(page, viewID string) string { return withFragment(page, "view="+url.QueryEscape(viewID)) }

func withFragment(page, frag string) string {
	if i := strings.IndexByte(page, '#'); i >= 0 {
		page = page[:i]
	}
	return page + "#" + frag
}

// ParseLink reads the fragment of a page address ("#edit=..." or "#view=..."): kind is "edit", "view" or "" for
// neither, and value the token or view ID.
func ParseLink(fragment string) (kind, value string) {
	fragment = strings.TrimPrefix(fragment, "#")
	for _, k := range []string{"edit", "view"} {
		if v, ok := strings.CutPrefix(fragment, k+"="); ok {
			if strings.ContainsAny(v, "&/ ?#") {
				return "", ""
			}
			if u, err := url.QueryUnescape(v); err == nil && u != "" {
				return k, u
			}
		}
	}
	return "", ""
}

// ---- the countdown ----

// WarnWithin is the time left below which the page warns that the document is about to go.
const WarnWithin = time.Hour

// Remaining is the time until the deadline (never negative); a zero deadline is zero.
func Remaining(deadline, now time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	return max(0, deadline.Sub(now))
}

// Countdown says a time left in words for the page: "3 h 12 min", "12 min", "under a minute"; "gone" at zero.
func Countdown(left time.Duration) string {
	switch {
	case left <= 0:
		return "gone"
	case left < time.Minute:
		return "under a minute"
	case left < time.Hour:
		return fmt.Sprintf("%d min", int(left/time.Minute))
	}
	h := int(left / time.Hour)
	m := int((left % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

// Warn says the document is in its last hour (and not yet gone).
func Warn(left time.Duration) bool { return left > 0 && left <= WarnWithin }
