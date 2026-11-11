package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageAndSecurityMiddleware(t *testing.T) {
	srv := httptest.NewServer(newRouter())
	defer srv.Close()

	t.Run("page renders and secure headers are present", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/?name=gojiberry")
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(string(body), "Hello, gojiberry!") {
			t.Fatalf("body missing rendered greeting: %s", body)
		}

		checks := map[string]string{
			"X-Frame-Options":           "DENY",
			"X-Content-Type-Options":    "nosniff",
			"X-Xss-Protection":          "1; mode=block",
			"Content-Security-Policy":   "default-src 'self'",
			"Referrer-Policy":           "strict-origin-when-cross-origin",
			"Strict-Transport-Security": "max-age=31536000; includeSubdomains",
		}
		for header, want := range checks {
			if got := resp.Header.Get(header); got != want {
				t.Errorf("header %s = %q, want %q", header, got, want)
			}
		}
	})

	t.Run("CORS allows the configured origin and rejects others", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/", nil)
		req.Header.Set("Origin", "https://trusted.example")
		req.Header.Set("Access-Control-Request-Method", "GET")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("OPTIONS /: %v", err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://trusted.example" {
			t.Errorf("Access-Control-Allow-Origin = %q, want the trusted origin echoed back", got)
		}

		req2, _ := http.NewRequest(http.MethodOptions, srv.URL+"/", nil)
		req2.Header.Set("Origin", "https://evil.example")
		req2.Header.Set("Access-Control-Request-Method", "GET")
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatalf("OPTIONS / (untrusted origin): %v", err)
		}
		resp2.Body.Close()
		if got := resp2.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q for an untrusted origin, want empty (not allowed)", got)
		}
	})

	t.Run("rate limiter genuinely rejects a client that exceeds it", func(t *testing.T) {
		limSrv := httptest.NewServer(newRouter())
		defer limSrv.Close()

		var last *http.Response
		for i := 0; i < 4; i++ {
			resp, err := http.Get(limSrv.URL + "/")
			if err != nil {
				t.Fatalf("GET / (request %d): %v", i, err)
			}
			resp.Body.Close()
			last = resp
		}
		if last.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("4th rapid request from the same client got status %d, want 429 (burst of 3 should be exhausted)", last.StatusCode)
		}
	})

	t.Run("a forged X-Forwarded-For does not walk around the rate limiter", func(t *testing.T) {
		limSrv := httptest.NewServer(newRouter())
		defer limSrv.Close()
		var last int
		for i := 0; i < 6; i++ {
			req, _ := http.NewRequest(http.MethodGet, limSrv.URL+"/", nil)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i)) // a new "client" each time
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			last = resp.StatusCode
		}
		if last != http.StatusTooManyRequests {
			t.Fatalf("6 rapid requests with 6 forged client addresses got status %d at the end, want 429: the header was trusted", last)
		}
	})
}
