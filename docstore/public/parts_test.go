package public

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFromEnv(t *testing.T) {
	env := map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC":                   "true",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES":     "1000",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_VERSIONS":      "7",
		"GOJIBERRY_DOCSTORE_PUBLIC_IP_DAILY_BYTES":    "99999",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_TTL":       "2h",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS":      "5",
		"GOJIBERRY_DOCSTORE_PUBLIC_WRITES_PER_MINUTE": "2.5",
		"GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL":         " https://x.example/t ",
		"GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN":       "a-token-of-16+chars",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY":        "a report key of some length",
		"GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES":   "10.0.0.0/8, 127.0.0.1,",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_TTL":        "48h",
	}
	c, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.MaxDocBytes != 1000 || c.MaxVersions != 7 || c.IPDailyBytes != 99999 || c.SessionTTL != 2*time.Hour ||
		c.MaxSessions != 5 || c.WritesPerMinute != 2.5 || c.TermsURL != "https://x.example/t" || c.AdminToken != "a-token-of-16+chars" ||
		len(c.TrustedProxies) != 2 || c.ReportTTL != 48*time.Hour {
		t.Fatalf("config = %+v", c)
	}
	d := c.withDefaults()
	if d.SessionMaxAge != DefaultSessionMaxAge || d.TombstoneTTL != DefaultRecordTTL || d.SessionsPerHour != DefaultSessionsPerHour {
		t.Fatalf("defaults = %+v", d)
	}
	c, err = FromEnv(func(string) string { return "" })
	if err != nil || c.Enabled {
		t.Fatalf("empty env: %+v, %v", c, err)
	}
	for k, v := range map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC":                  "maybe",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES":    "-1",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_TTL":      "tomorrow",
		"GOJIBERRY_DOCSTORE_PUBLIC_VIEWS_PER_MINUTE": "NaN",
	} {
		_, err := FromEnv(func(n string) string {
			if n == k {
				return v
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%q: err = %v", k, v, err)
		}
	}
}

func TestMemorySessionStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewMemorySessionStore(2)
	mk := func(exp time.Duration) Session { return Session{ID: "x", Created: now, Expires: now.Add(exp)} }
	if err := st.Create(ctx, "a", mk(time.Hour), now, SessionCaps{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "b", mk(time.Minute), now, SessionCaps{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "c", mk(time.Hour), now, SessionCaps{}); err != ErrSessionsFull {
		t.Fatalf("err = %v", err)
	}
	later := now.Add(2 * time.Minute) // b expired: room again
	if err := st.Create(ctx, "c", mk(time.Hour), later, SessionCaps{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get(ctx, "b", later); ok {
		t.Fatal("expired session found")
	}
	if ok, err := st.SetDoc(ctx, "a", "", "d1", now); !ok || err != nil {
		t.Fatalf("SetDoc = %v, %v", ok, err)
	}
	if ok, _ := st.SetDoc(ctx, "a", "", "d2", now); ok {
		t.Fatal("second claim won")
	}
	if ok, _ := st.SetDoc(ctx, "a", "d1", "", now); !ok {
		t.Fatal("release failed")
	}
	if _, err := st.SetDoc(ctx, "zzz", "", "d", now); err != ErrNoSession {
		t.Fatalf("err = %v", err)
	}
	_ = st.Extend(ctx, "a", now.Add(10*time.Minute), now)
	if s, _, _ := st.Get(ctx, "a", now); !s.Expires.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires %v", s.Expires)
	}
	_ = st.Delete(ctx, "a")
	_ = st.Delete(ctx, "a")
	if _, ok, _ := st.Get(ctx, "a", now); ok {
		t.Fatal("deleted session found")
	}
	if k := TokenKey("t"); len(k) != 64 || k == "t" {
		t.Fatalf("TokenKey = %q", k)
	}
}

func TestMemoryQuota(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewMemoryQuota(100, time.Hour)
	q.Now = func() time.Time { return now }
	if ok, _, _ := q.Take(ctx, "a", 60); !ok {
		t.Fatal("first refused")
	}
	ok, retry, _ := q.Take(ctx, "a", 60)
	if ok || retry != time.Hour {
		t.Fatalf("over budget: %v %v", ok, retry)
	}
	now = now.Add(20 * time.Minute)
	if ok, retry, _ := q.Take(ctx, "a", 60); ok || retry != 40*time.Minute {
		t.Fatalf("retry = %v", retry)
	}
	if ok, _, _ := q.Take(ctx, "a", 40); !ok { // a refused take charged nothing
		t.Fatal("exact fit refused")
	}
	if ok, _, _ := q.Take(ctx, "b", 100); !ok {
		t.Fatal("other key refused")
	}
	if ok, _, _ := q.Take(ctx, "b", 1); ok {
		t.Fatal("over by one")
	}
	now = now.Add(time.Hour)
	if ok, _, _ := q.Take(ctx, "a", 100); !ok {
		t.Fatal("window did not reset")
	}
	if ok, _, _ := (UnlimitedQuota{}).Take(ctx, "a", 1<<40); !ok {
		t.Fatal("UnlimitedQuota refused")
	}
}

func TestFromEnvHardeningVariables(t *testing.T) {
	env := map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_IDLE_TTL":          "30m",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_ADDRESS":  "4",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_HOUR":         "3",
		"GOJIBERRY_DOCSTORE_PUBLIC_ADDRESS_WRITES_PER_MINUTE": "50",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_TOTAL_BYTES":           "123456",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_REPORTS":               "77",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_REPORT_BYTES":          "4096",
		"GOJIBERRY_DOCSTORE_PUBLIC_REQUEST_TIMEOUT":           "5s",
		"GOJIBERRY_DOCSTORE_PUBLIC_FULL_SWEEP_INTERVAL":       "2h",
		"GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS":           "true",
	}
	c, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionIdleTTL != 30*time.Minute || c.MaxSessionsPerAddress != 4 || c.SessionsPerHour != 3 || c.WritesPerMinutePerAddress != 50 ||
		c.MaxTotalBytes != 123456 || c.MaxReports != 77 || c.MaxReportBytes != 4096 || c.RequestTimeout != 5*time.Second ||
		c.FullSweepInterval != 2*time.Hour || !c.AllowOpenDocs {
		t.Fatalf("config = %+v", c)
	}
	d := Config{}.withDefaults()
	if d.SessionIdleTTL != time.Hour || d.MaxSessionsPerAddress != DefaultMaxPerAddress || d.MaxTotalBytes != DefaultMaxTotalBytes ||
		d.MaxReports != DefaultMaxReports || d.RequestTimeout != DefaultRequestTimeout || d.SessionsPerHour != DefaultSessionsPerHour {
		t.Fatalf("defaults = %+v", d)
	}
	if c := (Config{SessionTTL: 10 * time.Minute}).withDefaults(); c.SessionIdleTTL != 10*time.Minute {
		t.Fatalf("the idle TTL exceeds the session TTL: %v", c.SessionIdleTTL)
	}
}

func TestMemoryQuotaRefund(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewMemoryQuota(100, time.Hour)
	q.Now = func() time.Time { return now }
	if ok, _, _ := q.Take(ctx, "a", 80); !ok {
		t.Fatal("refused")
	}
	if ok, _, _ := q.Take(ctx, "a", 80); ok {
		t.Fatal("over budget accepted")
	}
	var r QuotaRefunder = q
	r.Refund(ctx, "a", 80)
	r.Refund(ctx, "a", 500) // never below zero
	r.Refund(ctx, "nobody", 5)
	if ok, _, _ := q.Take(ctx, "a", 100); !ok {
		t.Fatal("the refund was not given back")
	}
}
