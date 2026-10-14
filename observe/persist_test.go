package observe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleBuiltin() *Builtin {
	b := NewBuiltin()
	o := New(Options{Operator: b})
	o.Add(Errors, 3, "code", "panic")
	o.Add(Errors, 2, "code", "panic")
	return b
}

func errorsCount(b *Builtin) int64 {
	series, _, _ := b.Snapshot()
	for _, s := range series {
		if s.Metric == Errors {
			return s.Count
		}
	}
	return 0
}

func TestTotalsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "totals.json")
	first := NewBuiltin()
	if err := first.KeepIn(path); err != nil {
		t.Fatalf("first start: %v", err)
	}
	o := New(Options{Operator: first})
	o.Add(Errors, 1, "code", "panic")
	o.Add(Errors, 1, "code", "panic")
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", fi, err)
	}
	second := NewBuiltin()
	if err := second.KeepIn(path); err != nil {
		t.Fatalf("second start: %v", err)
	}
	defer second.Close(context.Background())
	if got := errorsCount(second); got != 2 {
		t.Fatalf("the count after a restart is %d, want 2", got)
	}
	New(Options{Operator: second}).Add(Errors, 1, "code", "panic")
	if got := errorsCount(second); got != 3 {
		t.Fatalf("and it goes on from there: %d", got)
	}
}

func TestStoredTotalsThatAreNotTotalsAreRefused(t *testing.T) {
	good := string(sampleBuiltin().MarshalState())
	for name, bad := range map[string]string{
		"not json":         "{",
		"another version":  strings.Replace(good, `"v":1`, `"v":2`, 1),
		"unknown field":    strings.Replace(good, `"v":1`, `"v":1,"user":"ana"`, 1),
		"unknown metric":   strings.Replace(good, `"errors_total"`, `"secret_total"`, 1),
		"label off list":   strings.Replace(good, `"panic"`, `"ana@example.com"`, 1),
		"negative count":   strings.Replace(good, `"count":2`, `"count":-2`, 1),
		"usage source":     strings.Replace(good, `"source":"operator"`, `"source":"usage"`, 1),
		"trailing garbage": good + `{}`,
	} {
		b := NewBuiltin()
		if err := b.RestoreState([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if series, spans, _ := b.Snapshot(); len(series)+len(spans) != 0 {
			t.Errorf("%s: something was restored", name)
		}
	}
	if err := NewBuiltin().RestoreState([]byte(good)); err != nil {
		t.Fatalf("the good one: %v", err)
	}
}

func TestADamagedStateFileStartsTheTotalsAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "totals.json")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewBuiltin()
	err := b.KeepIn(path)
	defer b.Close(context.Background())
	if err == nil || errorsCount(b) != 0 {
		t.Fatalf("err=%v count=%d", err, errorsCount(b))
	}
}

func TestStatePathComesFromTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "totals.json")
	o, cfg, err := FromEnv(context.Background(), env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_STATE", path))
	if err != nil || cfg.StatePath != path {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	o.Add(Errors, 1, "code", "panic")
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("closing the observer writes the totals: %v", err)
	}
	// Without the builtin sink there is nothing to keep, and the operator is told.
	cfg, _ = ParseEnv(env("GOJIBERRY_OBS", "on", "GOJIBERRY_OBS_SINKS", "posthog", "GOJIBERRY_OBS_STATE", path))
	if cfg.StatePath != "" || len(cfg.Notes) == 0 {
		t.Fatalf("cfg=%+v", cfg)
	}
}
