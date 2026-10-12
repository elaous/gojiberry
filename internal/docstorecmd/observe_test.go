package docstorecmd

import (
	"context"
	"strings"
	"testing"

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
