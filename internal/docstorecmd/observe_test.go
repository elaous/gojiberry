package docstorecmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/observe"
)

func obsEnv(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestObserverIsOffUnlessTheOperatorSetsIt(t *testing.T) {
	t.Cleanup(func() { observe.SetDefault(nil) })
	for _, e := range []func(string) string{
		obsEnv(),
		obsEnv("GOJIBERRY_OBS", "off", "GOJIBERRY_OBS_SINKS", "builtin"),
		obsEnv("GOJIBERRY_OBS_SINKS", "builtin"),
	} {
		o, err := setupObserver(context.Background(), e)
		if err != nil || o != nil || observe.Default() != nil {
			t.Fatalf("o=%v err=%v default=%v: want nothing", o, err, observe.Default())
		}
	}
}

func TestObserverOnIsTheProcessDefaultAndBadSettingsFailAtStart(t *testing.T) {
	t.Cleanup(func() { observe.SetDefault(nil) })
	if _, err := setupObserver(context.Background(), obsEnv("GOJIBERRY_OBS", "yes-please")); err == nil || !strings.Contains(err.Error(), "GOJIBERRY_OBS") || strings.Contains(err.Error(), "yes-please") {
		t.Fatalf("err = %v", err)
	}
	o, err := setupObserver(context.Background(), obsEnv("GOJIBERRY_OBS", "on"))
	if err != nil || !o.Enabled() || observe.Default() != o {
		t.Fatalf("o=%v err=%v", o, err)
	}
}

// phCapture stands in for PostHog and keeps the event names and properties it is sent.
type phCapture struct {
	mu     sync.Mutex
	events []map[string]any
}

func (c *phCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body struct {
		Batch []map[string]any `json:"batch"`
	}
	_ = json.Unmarshal(b, &body)
	c.mu.Lock()
	c.events = append(c.events, body.Batch...)
	c.mu.Unlock()
}

func (c *phCapture) usage() (n float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e["event"] == "gojiberry.usage_events_total" {
			n += e["properties"].(map[string]any)["value"].(float64)
		}
	}
	return n
}

func (c *phCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

// TestUsageCountsReachPostHogOnlyWhenEveryConsentIsGiven: the person's opt-in is the browser sending a batch at
// all; the operator's are the receiver (GOJIBERRY_DOCSTORE_TELEMETRY=on), observability (GOJIBERRY_OBS=on)
// and naming the sink for usage counts (GOJIBERRY_OBS_USAGE_SINKS).
func TestUsageCountsReachPostHogOnlyWhenEveryConsentIsGiven(t *testing.T) {
	t.Cleanup(func() { observe.SetDefault(nil) })
	day := time.Now().UTC().Format("2006-01-02")
	batch := `{"day":"` + day + `","counts":[{"name":"feature","value":"find","n":6}]}`
	run := func(t *testing.T, receiver string, env ...string) *phCapture {
		t.Helper()
		ph := &phCapture{}
		srv := httptest.NewServer(ph)
		defer srv.Close()
		o, err := setupObserver(context.Background(), obsEnv(append([]string{"GOJIBERRY_OBS_POSTHOG_HOST", srv.URL, "GOJIBERRY_OBS_POSTHOG_KEY", "phc_test"}, env...)...))
		if err != nil {
			t.Fatal(err)
		}
		s := docstore.NewServer(docstore.NewMemoryAdapter(), "memory").WithObserver(o)
		withTelemetry(s, receiver)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/telemetry", strings.NewReader(batch)))
		if receiver == "on" && rec.Code != http.StatusNoContent {
			t.Fatalf("the receiver answered %d", rec.Code)
		}
		if err := o.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		return ph
	}
	t.Run("everything on", func(t *testing.T) {
		if got := run(t, "on", "GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "builtin", "GOJIBERRY_OBS_USAGE_SINKS", "posthog").usage(); got != 6 {
			t.Fatalf("usage reaching PostHog = %v, want 6", got)
		}
	})
	t.Run("receiver off", func(t *testing.T) {
		if ph := run(t, "off", "GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_USAGE_SINKS", "posthog"); ph.count() != 0 {
			t.Fatalf("%d events reached PostHog with the receiver off (and no operator sink)", ph.count())
		}
	})
	t.Run("observability off", func(t *testing.T) {
		if ph := run(t, "on", "GOJIBERRY_OBS_USAGE_SINKS", "posthog"); ph.count() != 0 {
			t.Fatalf("%d events reached PostHog with GOJIBERRY_OBS unset", ph.count())
		}
	})
	t.Run("sink not named for usage", func(t *testing.T) {
		if got := run(t, "on", "GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "posthog").usage(); got != 0 {
			t.Fatalf("usage reached PostHog without GOJIBERRY_OBS_USAGE_SINKS: %v", got)
		}
	})
}
