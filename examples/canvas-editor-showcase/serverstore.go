package main

import (
	"context"
	"net/http"
)

type bearerTransport struct {
	token func(ctx context.Context) (string, error)
	next  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != nil {
		tok, err := t.token(req.Context())
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req = req.Clone(req.Context()) // a RoundTripper must not change the request it is given
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(req)
}
