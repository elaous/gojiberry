package docstorecmd

import (
	"testing"

	"github.com/elaous/gojiberry/a11ycheck"
)

func TestExportGateSettingIsParsedAndATypoStopsTheServer(t *testing.T) {
	for in, want := range map[string]a11ycheck.GateMode{
		"": a11ycheck.GateAllow, "allow": a11ycheck.GateAllow, "warn": a11ycheck.GateWarn, " BLOCK ": a11ycheck.GateBlock,
	} {
		got, err := exportGate(in)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"blok", "on", "true", "strict"} {
		if _, err := exportGate(in); err == nil {
			t.Errorf("%q was accepted: a typo must not run the server without the gate", in)
		}
	}
}
