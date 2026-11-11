package encrypt

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func TestLogReadsAndLookupsHaveSeparateSharedBudgets(t *testing.T) {
	mk := func(spent string) *dirEnv {
		return newDirEnvWith(t, nil, func(s *docstore.Server) {
			s.WithDirectoryLimits(func(name string, perMinute float64, burst int) docstore.Limiter {
				if name == spent+"-ip" || strings.HasPrefix(name, spent+"-all-") {
					return docstore.NewRateLimiter(0.001, 1) // one read, then refused
				}
				return docstore.NewRateLimiter(perMinute, burst)
			})
		})
	}
	user := auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}

	e := mk("dir-log")
	tok := e.token(user)
	if rec := e.do("GET", "/directory/log", tok, ""); rec.Code != 200 {
		t.Fatalf("first log read = %d", rec.Code)
	}
	if rec := e.do("GET", "/directory/log", tok, ""); rec.Code != 429 {
		t.Errorf("second log read = %d, want 429 from the log's own shared budget", rec.Code)
	}
	for i := range 5 {
		if code, _ := e.lookup(tok, "bo@x.io"); code != 200 {
			t.Fatalf("lookup %d after the log budget was spent = %d, want 200: the log must not use up lookups", i, code)
		}
	}

	e = mk("dir-lookup")
	tok = e.token(user)
	if code, _ := e.lookup(tok, "bo@x.io"); code != 200 {
		t.Fatalf("first lookup = %d", code)
	}
	if code, _ := e.lookup(tok, "bo@x.io"); code != 429 {
		t.Errorf("second lookup = %d, want 429 from the lookups' own shared budget", code)
	}
	for i := range 3 {
		if rec := e.do("GET", "/directory/log", tok, ""); rec.Code != 200 {
			t.Fatalf("log read %d after the lookup budget was spent = %d, want 200: lookups must not use up the log", i, rec.Code)
		}
	}
}
