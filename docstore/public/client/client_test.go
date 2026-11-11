package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/public"
	"github.com/elaous/gojiberry/docstore/public/client"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

// offer starts the real public service over a test server.
func offer(t *testing.T, mod func(*public.Options)) (*client.Client, *clock, *httptest.Server) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	ea := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
	ea.Now = clk.Now
	o := public.Options{Store: ea, Now: clk.Now, Config: public.Config{
		AdminToken: "admin-token-for-tests", NoProxy: true, TermsURL: "https://example.org/terms",
		SessionsPerHour: 1000, MaxSessionsPerAddress: 1000, WritesPerMinute: 1000, WritesPerMinutePerAddress: 100000, ViewsPerMinute: 1000, ReportsPerMinute: 1000,
	}}
	if mod != nil {
		mod(&o)
	}
	svc, err := public.New(o)
	if err != nil {
		t.Fatal(err)
	}
	srv := docstore.NewServer(ea, "memory")
	svc.Mount(srv)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &client.Client{BaseURL: ts.URL}, clk, ts
}

func TestTheWholeVisit(t *testing.T) {
	ctx := context.Background()
	c, clk, _ := offer(t, nil)
	if _, err := c.Create(ctx, []byte("x")); !errors.Is(err, client.ErrNoSession) {
		t.Fatalf("no session yet: %v", err)
	}
	if err := c.NewSession(ctx); err != nil || c.Token == "" {
		t.Fatalf("%v", err)
	}
	doc, err := c.Create(ctx, []byte("first draft"))
	if err != nil || doc.ID == "" || doc.ViewID == "" || doc.ViewID == doc.ID {
		t.Fatalf("%+v %v", doc, err)
	}
	if left := client.Remaining(doc.Expires, clk.Now()); left != 24*time.Hour {
		t.Errorf("a fresh document has 24 hours: %v", left)
	}
	if c.Terms != "https://example.org/terms" {
		t.Errorf("terms %q", c.Terms)
	}
	if _, err := c.Create(ctx, []byte("another")); !errors.Is(err, client.ErrExists) {
		t.Errorf("%v", err)
	}
	clk.t = clk.t.Add(10 * time.Hour)
	exp, err := c.Save(ctx, []byte("second draft"))
	if err != nil || client.Remaining(exp, clk.Now()) != 24*time.Hour {
		t.Fatalf("%v %v", exp, err)
	}
	clk.t = clk.t.Add(5 * time.Hour)
	got, d2, err := c.Download(ctx)
	if err != nil || string(got) != "second draft" || d2.ViewID != doc.ViewID || client.Remaining(d2.Expires, clk.Now()) != 19*time.Hour {
		t.Fatalf("%q %+v %v", got, d2, err)
	}
	viewer := &client.Client{BaseURL: c.BaseURL}
	seen, vexp, err := viewer.View(ctx, doc.ViewID)
	if err != nil || string(seen) != "second draft" || !vexp.Equal(d2.Expires) {
		t.Fatalf("%q %v %v", seen, vexp, err)
	}
	if _, _, err := viewer.View(ctx, "no-such-view"); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("%v", err)
	}
	other := &client.Client{BaseURL: c.BaseURL, Token: c.Token}
	if _, _, err := other.Download(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Discard(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := viewer.View(ctx, doc.ViewID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("a discarded document is gone: %v", err)
	}
	if _, err := c.Create(ctx, []byte("new")); err != nil {
		t.Errorf("%v", err)
	}
}

func TestRefusalsAreTypedWithTheWait(t *testing.T) {
	ctx := context.Background()
	c, _, _ := offer(t, func(o *public.Options) { o.Config.WritesPerMinute = 2; o.Config.MaxDocBytes = 64 })
	if err := c.NewSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(ctx, make([]byte, 65)); !errors.Is(err, client.ErrTooLarge) {
		t.Fatalf("%v", err)
	}
	if _, err := c.Create(ctx, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	_, err := c.Save(ctx, []byte("too soon"))
	var e *client.Error
	if !errors.As(err, &e) || !errors.Is(err, client.ErrRateLimited) || e.Status != http.StatusTooManyRequests || e.RetryAfter <= 0 {
		t.Fatalf("%v", err)
	}
	bad := &client.Client{BaseURL: c.BaseURL, Token: "not-a-token"}
	if _, _, err := bad.Download(ctx); !errors.Is(err, client.ErrForbidden) && !errors.Is(err, client.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestExpiryAndTakedownAreTold(t *testing.T) {
	ctx := context.Background()
	c, clk, ts := offer(t, nil)
	if err := c.NewSession(ctx); err != nil {
		t.Fatal(err)
	}
	doc, err := c.Create(ctx, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(25 * time.Hour)
	if _, err := c.Save(ctx, []byte("late")); !errors.Is(err, client.ErrNotFound) && !errors.Is(err, client.ErrForbidden) {
		t.Errorf("an expired document is not revived (and its idle session may be gone too): %v", err)
	}
	c2 := &client.Client{BaseURL: ts.URL}
	if err := c2.NewSession(ctx); err != nil {
		t.Fatal(err)
	}
	d2, err := c2.Create(ctx, []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/admin/takedown/"+d2.ViewID, nil)
	req.Header.Set("X-Gojiberry-Admin-Token", "admin-token-for-tests")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode/100 != 2 {
		t.Fatalf("%v %v", err, resp)
	}
	_ = resp.Body.Close()
	if _, _, err := c2.View(ctx, d2.ViewID); !errors.Is(err, client.ErrGone) {
		t.Errorf("%v", err)
	}
	_ = doc
}

func TestReportsReachTheOperator(t *testing.T) {
	ctx := context.Background()
	c, _, _ := offer(t, nil)
	if err := c.NewSession(ctx); err != nil {
		t.Fatal(err)
	}
	doc, err := c.Create(ctx, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &client.Client{BaseURL: c.BaseURL}
	if err := reader.Report(ctx, doc.ViewID, client.CategorySpam, "it is spam"); err != nil {
		t.Fatal(err)
	}
	if err := reader.Report(ctx, doc.ViewID, client.CategorySpam, "it is spam"); err != nil {
		t.Fatal(err)
	}
}

func TestLinksRoundTripAndTheCountdownSpeaks(t *testing.T) {
	page := "https://host/app/?lang=de#old"
	e, v := client.EditLink(page, "tok+/en"), client.ViewLink(page, "view-id")
	if e != "https://host/app/?lang=de#edit=tok%2B%2Fen" || v != "https://host/app/?lang=de#view=view-id" {
		t.Errorf("%s %s", e, v)
	}
	for frag, want := range map[string][2]string{
		"#edit=tok%2B%2Fen": {"edit", "tok+/en"},
		"view=abc":          {"view", "abc"},
		"#other=1":          {"", ""},
		"#edit=":            {"", ""},
		"#edit=a/b":         {"", ""},
		"":                  {"", ""},
	} {
		if k, val := client.ParseLink(frag); k != want[0] || val != want[1] {
			t.Errorf("%q -> %q %q", frag, k, val)
		}
	}
	for d, want := range map[time.Duration]string{
		0: "gone", 30 * time.Second: "under a minute", 12 * time.Minute: "12 min",
		3*time.Hour + 12*time.Minute: "3 h 12 min", 5 * time.Hour: "5 h", 24 * time.Hour: "24 h",
	} {
		if got := client.Countdown(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if !client.Warn(59*time.Minute) || client.Warn(61*time.Minute) || client.Warn(0) {
		t.Error("the warning is for the last hour, until it is gone")
	}
	if client.Remaining(time.Time{}, time.Now()) != 0 || client.Remaining(time.Now().Add(-time.Hour), time.Now()) != 0 {
		t.Error("never negative")
	}
}

func TestABusyServerAsksForProofOfWorkAndTheClientAnswers(t *testing.T) {
	c, _, _ := offer(t, func(o *public.Options) {
		o.Challenger = &public.ProofOfWork{Secret: []byte("0123456789abcdef"), Difficulty: 8}
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("the client solves the challenge and retries: %v", err)
	}
	if c.Token == "" {
		t.Fatal("a session was issued")
	}
	if _, err := c.Create(context.Background(), []byte("x")); err != nil {
		t.Fatalf("a create is challenged too: %v", err)
	}
}
