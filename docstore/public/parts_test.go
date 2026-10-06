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
		"GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN":       "tok",
		"GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES":   "10.0.0.0/8, 127.0.0.1,",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_TTL":        "48h",
	}
	c, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.MaxDocBytes != 1000 || c.MaxVersions != 7 || c.IPDailyBytes != 99999 || c.SessionTTL != 2*time.Hour ||
		c.MaxSessions != 5 || c.WritesPerMinute != 2.5 || c.TermsURL != "https://x.example/t" || c.AdminToken != "tok" ||
		len(c.TrustedProxies) != 2 || c.ReportTTL != 48*time.Hour {
		t.Fatalf("config = %+v", c)
	}
	d := c.withDefaults()
	if d.SessionMaxAge != DefaultSessionMaxAge || d.TombstoneTTL != DefaultRecordTTL || d.SessionsPerMinute != DefaultSessionsPerMinute {
		t.Fatalf("defaults = %+v", d)
	}
	// off by default
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
	if err := st.Create(ctx, "a", mk(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "b", mk(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, "c", mk(time.Hour), now); err != ErrSessionsFull {
		t.Fatalf("err = %v", err)
	}
	later := now.Add(2 * time.Minute) // b expired: room again
	if err := st.Create(ctx, "c", mk(time.Hour), later); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get(ctx, "b", later); ok {
		t.Fatal("expired session found")
	}
	// SetDoc is a compare-and-set
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
	// Extend only moves forward
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
