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

func TestParsePrefsLanguageThemeAndAutoClose(t *testing.T) {
	p, err := parsePrefs([]byte(`{"lang":"pt-BR","theme":"dark","close":"off"}`))
	if err != nil || p.Lang != "pt-BR" || p.Theme != "dark" || p.Close != "off" {
		t.Errorf("%+v %v", p, err)
	}
	p, _ = parsePrefs([]byte(`{"lang":"../etc","theme":"purple","close":"maybe"}`))
	if p.Lang != "" || p.Theme != "" || p.Close != "" {
		t.Errorf("odd values kept: %+v", p)
	}
	for _, ok := range []string{"en", "de", "pt-BR", "zh-Hant-TW"} {
		if !validLang(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-en", "en-", "e n", "1en", "averyveryverylongtag"} {
		if validLang(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParsePrefsColourBlindSafeColours(t *testing.T) {
	p, _ := parsePrefs([]byte(`{"colours":"safe"}`))
	if len(p.peerPalette()) < 4 {
		t.Errorf("no palette for the safe setting: %v", p.peerPalette())
	}
	p, _ = parsePrefs([]byte(`{"colours":"rainbow"}`))
	if p.Colours != "" || p.peerPalette() != nil {
		t.Errorf("an unknown value was kept: %+v", p)
	}
}

func TestTextScaleFollowsTheTextSizeSetting(t *testing.T) {
	for in, want := range map[string]float64{"": 1, "large": 1.25, "larger": 1.5, "largest": 2, "huge": 1} {
		p, _ := parsePrefs([]byte(`{"text":"` + in + `"}`))
		if got := p.textScale(); got != want {
			t.Errorf("text %q: scale %g, want %g", in, got, want)
		}
	}
}
