//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"
)

func TestParsePrefsKeepsWhatIsKnownAndDropsTheRest(t *testing.T) {
	p, err := parsePrefs([]byte(`{"text":"larger","contrast":"more","motion":"sideways","memory":"high","speed":"fast","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "larger" || p.Contrast != "more" || p.Memory != "high" || p.Speed != "fast" {
		t.Errorf("known values lost: %+v", p)
	}
	if p.Motion != "" {
		t.Errorf("an unknown motion value was kept: %q", p.Motion)
	}
}

func TestParsePrefsRefusesWhatIsNotAPreferencesFile(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "text": "hello", "array": "[1,2]", "huge": `{"text":"` + strings.Repeat("x", maxPrefsFile) + `"}`} {
		if _, err := parsePrefs([]byte(in)); err == nil && name != "empty" {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEncodedPrefsReadBackTheSame(t *testing.T) {
	want := prefs{Text: "large", Spacing: "wide", Collab: "off", Memory: "low", Where: "on"}
	got, err := parsePrefs(encodePrefs(want))
	if err != nil || got != want {
		t.Errorf("round trip: %+v %v, want %+v", got, err, want)
	}
}
