package main

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/speechcoach"
)

type speechVault map[string][]byte

func (m speechVault) Get(n string) ([]byte, error) { return m[n], nil }
func (m speechVault) Put(n string, d []byte) error { m[n] = append([]byte(nil), d...); return nil }
func (m speechVault) Delete(n string) error        { delete(m, n); return nil }
func (m speechVault) Names(prefix string) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func takeOf(prompt string, n int) speechcoach.Entry {
	pcm := make([]int16, 1600)
	for i := range pcm {
		pcm[i] = int16((i * n) % 3000)
	}
	return speechcoach.NewEntry("en", prompt, prompt, speechcoach.Prompted, pcm, nil, time.Unix(1700000000, 0))
}

// Kept recordings come back whole under the same key; nothing readable is in the vault; a retake replaces the
// old take in the vault; a changed one, or one sealed under another key, is dropped.
func TestKeptRecordingsAreSealedAndComeBack(t *testing.T) {
	key, _ := speechSealKey([]byte("device secret"))
	other, _ := speechSealKey([]byte("another secret"))
	v := speechVault{}
	var d speechcoach.Dataset
	d.Add(takeOf("good morning", 1))
	d.Add(takeOf("thank you", 2))
	kept := map[string]bool{}
	if err := keepSpeech(v, key, &d, kept); err != nil {
		t.Fatal(err)
	}
	for name, b := range v {
		if strings.Contains(string(b), "good morning") || strings.Contains(string(b), "RIFF") {
			t.Errorf("%s holds readable data", name)
		}
	}
	// a retake of one phrase replaces its take: the old sealed entry leaves the vault
	d.Add(takeOf("good morning", 3))
	if err := keepSpeech(v, key, &d, kept); err != nil {
		t.Fatal(err)
	}
	if n := len(v.Names("speech/e/")); n != 2 {
		t.Errorf("the vault holds %d recordings, want 2: %v", n, v.Names("speech/"))
	}
	var back speechcoach.Dataset
	n, err := loadSpeech(v, key, &back, map[string]bool{})
	if err != nil || n != 2 || back.Len() != 2 {
		t.Fatalf("loaded %d (%v), dataset %d", n, err, back.Len())
	}
	if e := back.ByPrompt("good morning"); e == nil || e.SHA256 != takeOf("good morning", 3).SHA256 {
		t.Errorf("the retake did not come back: %+v", e)
	}

	// one changed recording is dropped, the other stays
	for _, name := range v.Names("speech/e/") {
		b := v[name]
		b[len(b)-1] ^= 1
		break
	}
	var partial speechcoach.Dataset
	if n, err := loadSpeech(v, key, &partial, map[string]bool{}); err != nil || n != 1 {
		t.Errorf("after a change: loaded %d (%v), want 1", n, err)
	}
	if len(v.Names("speech/e/")) != 1 {
		t.Errorf("the changed one should have been removed: %v", v.Names("speech/"))
	}

	// another key opens nothing, and the vault is wiped
	var wrong speechcoach.Dataset
	if n, err := loadSpeech(v, other, &wrong, map[string]bool{}); err == nil || n != 0 || wrong.Len() != 0 {
		t.Errorf("another key: %d %v", n, err)
	}
	if len(v) != 0 {
		t.Errorf("the vault was not wiped: %v", v.Names(""))
	}
}

// Deleting everything empties the vault; corrections are kept sealed and come back.
func TestKeptCorrectionsAndDeletion(t *testing.T) {
	key, _ := speechSealKey([]byte("device secret"))
	v := speechVault{}
	var d speechcoach.Dataset
	d.Add(takeOf("hello", 1))
	kept := map[string]bool{}
	c := &speechcoach.Corrections{}
	c.Learn("meet me at the whether station", "meet me at the weather station")
	if err := keepSpeech(v, key, &d, kept); err != nil {
		t.Fatal(err)
	}
	if err := keepCorrections(v, key, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(v[keptSpeechCorr]), "weather") {
		t.Error("the corrections are readable in the vault")
	}
	back := loadCorrections(v, key)
	if back.Len() != c.Len() || len(back.Suggest("the whether report")) == 0 {
		t.Errorf("corrections came back as %d pairs", back.Len())
	}
	if got := loadCorrections(v, mustKey(t, "another")); got.Len() != 0 {
		t.Error("another key read the corrections")
	}
	d.Clear()
	if err := keepSpeech(v, key, &d, kept); err != nil {
		t.Fatal(err)
	}
	if n := len(v.Names("speech/e/")); n != 0 {
		t.Errorf("recordings remain after delete: %v", v.Names("speech/"))
	}
	wipeSpeech(v)
	if len(v) != 0 {
		t.Errorf("wipe left %v", v.Names(""))
	}
}

func mustKey(t *testing.T, s string) []byte {
	k, err := speechSealKey([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return k
}
