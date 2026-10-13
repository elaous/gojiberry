package observe

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// withFactory registers a sink factory for the length of a test and counts its calls.
func withFactory(t *testing.T, name string, s Sink, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	regMu.Lock()
	old, had := factories[name]
	factories[name] = func(context.Context, SinkEnv) (Sink, error) { calls.Add(1); return s, err }
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		defer regMu.Unlock()
		if had {
			factories[name] = old
		} else {
			delete(factories, name)
		}
	})
	return &calls
}

func TestOffByDefaultBuildsNothingAndReadsNothing(t *testing.T) {
	calls := withFactory(t, "otlp", &rec{}, nil)
	read := false
	for _, e := range []func(string) string{
		env(),
		env("GOJIBERRY_OBS", "off", "GOJIBERRY_OBS_SINKS", "otlp,posthog", "GOJIBERRY_OBS_POSTHOG_KEY_FILE", "/run/secrets/k"),
		// a bad value cannot stop a server that has the switch off
		env("GOJIBERRY_OBS_SINKS", "not-a-sink", "GOJIBERRY_OBS_TRACE_SAMPLE", "lots"),
	} {
		o, cfg, err := fromEnv(context.Background(), e, func(string) ([]byte, error) { read = true; return nil, nil }, ServiceName)
		if err != nil || o != nil || cfg.Enabled {
			t.Fatalf("o=%v cfg=%+v err=%v: want nothing", o, cfg, err)
		}
	}
	if calls.Load() != 0 || read {
		t.Fatalf("a sink was built (%d) or a file read (%v) with observability off", calls.Load(), read)
	}
	if o, _, _ := FromEnv(context.Background(), env("GOJIBERRY_OBS_SINKS", "posthog")); o.Enabled() {
		t.Fatal("enabled by the sinks setting alone")
	}
}

func TestIgnoredVariablesAreSaidWhenOff(t *testing.T) {
	cfg, _ := ParseEnv(env("GOJIBERRY_OBS_SINKS", "posthog", "GOJIBERRY_OBS_POSTHOG_KEY", "phc_x"))
	if len(cfg.Notes) != 2 || !strings.Contains(cfg.Notes[0]+cfg.Notes[1], "GOJIBERRY_OBS_POSTHOG_KEY is ignored") {
		t.Fatalf("notes = %v", cfg.Notes)
	}
	for _, n := range cfg.Notes {
		if strings.Contains(n, "phc_x") {
			t.Fatal("a note repeats a value")
		}
	}
}

func TestOnDefaultsToTheBuiltinSink(t *testing.T) {
	o, cfg, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", " ON "))
	if err != nil || !o.Enabled() {
		t.Fatalf("o=%v err=%v", o, err)
	}
	if len(cfg.Sinks) != 1 || cfg.Sinks[0] != "builtin" || len(cfg.UsageSinks) != 0 || cfg.TraceSample != DefaultTraceSample {
		t.Fatalf("cfg = %+v", cfg)
	}
	o.Add(Errors, 1, "code", "panic")
	d := o.Describe()
	if !d.Enabled || len(d.SinkStatus["builtin"].(map[string]any)["series"].([]Series)) != 1 {
		t.Fatalf("describe = %+v", d)
	}
}

func TestInvalidSettingsNameTheVariableNotTheValue(t *testing.T) {
	for _, c := range []struct {
		kv   []string
		name string
		leak string
	}{
		{[]string{"GOJIBERRY_OBS", "maybe-secret"}, "GOJIBERRY_OBS", "maybe-secret"},
		{[]string{"GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "builtin,carrierpigeon"}, "GOJIBERRY_OBS_SINKS", "carrierpigeon"},
		{[]string{"GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_USAGE_SINKS", "tcp"}, "GOJIBERRY_OBS_USAGE_SINKS", "tcp"},
		{[]string{"GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_TRACE_SAMPLE", "7"}, "GOJIBERRY_OBS_TRACE_SAMPLE", "7"},
		{[]string{"GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_TRACE_SAMPLE", "abc"}, "GOJIBERRY_OBS_TRACE_SAMPLE", "abc"},
	} {
		_, _, err := FromEnv(context.Background(), env(c.kv...))
		if err == nil || !strings.Contains(err.Error(), c.name) || strings.Contains(err.Error(), c.leak) {
			t.Errorf("%v: err = %v", c.kv, err)
		}
	}
}

func TestASinkThatThisBuildLacksIsAnErrorAtStart(t *testing.T) {
	regMu.Lock()
	old, had := factories["otlp"]
	delete(factories, "otlp")
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		defer regMu.Unlock()
		if had {
			factories["otlp"] = old
		}
	})
	_, _, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "otlp"))
	if err == nil || !strings.Contains(err.Error(), "otlp") || !strings.Contains(err.Error(), "GOJIBERRY_OBS_SINKS") {
		t.Fatalf("err = %v", err)
	}
}

func TestSinksAreRoutedPerProducer(t *testing.T) {
	ext := &rec{}
	withFactory(t, "otlp", ext, nil)
	// operator metrics to builtin only, usage counts to otlp
	o, _, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "builtin", "GOJIBERRY_OBS_USAGE_SINKS", "builtin, otlp"))
	if err != nil {
		t.Fatal(err)
	}
	o.SetUsageAllowlist(map[string][]string{"feature": {"find"}})
	o.Add(Errors, 1, "code", "panic")
	o.Usage("feature", "find", 2)
	pts, _ := ext.snapshot()
	if len(pts) != 1 || pts[0].Metric != UsageEvents {
		t.Fatalf("the external sink got %+v, want only the usage count", pts)
	}
	// operator metrics to otlp only: usage goes nowhere external
	ext2 := &rec{}
	withFactory(t, "otlp", ext2, nil)
	o, _, err = FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "otlp"))
	if err != nil {
		t.Fatal(err)
	}
	o.SetUsageAllowlist(map[string][]string{"feature": {"find"}})
	o.Add(Errors, 1, "code", "panic")
	o.Usage("feature", "find", 2)
	pts, _ = ext2.snapshot()
	if len(pts) != 1 || pts[0].Metric != Errors {
		t.Fatalf("the external sink got %+v, want only the operator metric", pts)
	}
}

func TestAFailingFactoryClosesWhatWasBuilt(t *testing.T) {
	first := &rec{}
	withFactory(t, "otlp", first, nil)
	withFactory(t, "posthog", nil, errors.New("observe: GOJIBERRY_OBS_POSTHOG_KEY is missing"))
	_, _, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "otlp,posthog"))
	if err == nil || !strings.Contains(err.Error(), "GOJIBERRY_OBS_POSTHOG_KEY") {
		t.Fatalf("err = %v", err)
	}
	if first.closed != 1 {
		t.Fatal("the sink built before the failure was left running")
	}
}

func TestReadSecretConventions(t *testing.T) {
	read := func(p string) ([]byte, error) {
		switch p {
		case "/s":
			return []byte("value\n"), nil
		case "/big":
			return make([]byte, 5000), nil
		}
		return nil, errors.New("open /nope: permission denied")
	}
	for _, c := range []struct {
		kv   []string
		want string
		err  string
	}{
		{[]string{"K", "plain"}, "plain", ""},
		{[]string{"K_FILE", "/s"}, "value", ""},
		{[]string{"K", "a", "K_FILE", "/s"}, "", "not both"},
		{[]string{"K_FILE", "/nope"}, "", "K_FILE"},
		{[]string{"K_FILE", "/big"}, "", "too large"},
		{nil, "", ""},
	} {
		got, err := ReadSecret(SinkEnv{Getenv: env(c.kv...), ReadFile: read}, "K")
		if got != c.want || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%v: got %q err %v", c.kv, got, err)
		}
		if err != nil && (strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "/nope")) {
			t.Errorf("the error repeats the cause: %v", err)
		}
	}
}
