package directoryclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// pathClient rewrites the body of the answers for one path.
func pathClient(path string, f func([]byte) []byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err != nil || r.URL.Path != path {
			return res, err
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(f(body)))
		res.ContentLength = -1
		return res, nil
	})}
}

func TestWatchLogPinsThePseudonymWithTheClientsOwnEntries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	pub, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id}); err != nil {
		t.Fatal(err)
	}
	if len(c.State.Made) != 1 {
		t.Fatalf("the client's own entry is noted: %+v", c.State)
	}
	liar := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, HTTP: pathClient("/directory/log/address", func([]byte) []byte {
		return []byte(`{"address":"` + strings.Repeat("ab", 32) + `"}`)
	})}
	before := *c.State
	before.Made = append([]MadeEntry(nil), c.State.Made...)
	if _, err := liar.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{}); !errors.Is(err, ErrAddressMismatch) {
		t.Fatalf("a pseudonym that does not hold the client's own entry: %v", err)
	}
	if c.State.Seq != before.Seq || len(c.State.Made) != 1 {
		t.Errorf("the state changes only when everything verified: %+v", c.State)
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil || rep.AddressUnconfirmed {
		t.Fatalf("%+v %v", rep, err)
	}
	if len(c.State.Addresses) != 1 || c.State.Addresses[0] != e.dir.LogAddressFor("ana@x.io") {
		t.Fatalf("the confirmed pseudonym is pinned: %+v", c.State.Addresses)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil || rep.AddressUnconfirmed || len(c.State.Addresses) != 1 {
		t.Errorf("%+v %v %+v", rep, err, c.State)
	}
}

func TestBaseURLMustBeHTTPSExceptLocalhost(t *testing.T) {
	ctx := context.Background()
	for _, base := range []string{"http://dir.example.com", "ftp://x", "dir.example.com", "http://127.0.0.1.evil.com"} {
		c := &Client{BaseURL: base, Token: func() string { return "t" }}
		if _, err := c.Lookup(ctx, "ana@x.io"); err == nil || !strings.Contains(err.Error(), "BaseURL") {
			t.Errorf("%q accepted: %v", base, err)
		}
	}
	for _, base := range []string{"", "/api", "https://dir.example.com", "http://localhost:8080", "http://127.0.0.1:9", "http://[::1]:9"} {
		if err := checkBaseURL(base); err != nil {
			t.Errorf("%q refused: %v", base, err)
		}
	}
	if err := checkBaseURL("http://dir.example.com"); !errors.Is(err, ErrInsecureBaseURL) {
		t.Errorf("%v", err)
	}
}
