package piper

import (
	"context"
	"crypto/md5" //nolint:gosec // the test makes the sums the catalogue publishes
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/voice/read"
)

type fakeFetcher struct {
	files map[string][]byte // by URL
	asked []string
	fail  map[string]error
}

func (f *fakeFetcher) Fetch(_ context.Context, url string, progress func(received, total int64), done func([]byte, error)) {
	f.asked = append(f.asked, url)
	if err := f.fail[url]; err != nil {
		done(nil, err)
		return
	}
	data, ok := f.files[url]
	if !ok {
		done(nil, errors.New("404"))
		return
	}
	if progress != nil {
		progress(int64(len(data))/2, int64(len(data)))
		progress(int64(len(data)), int64(len(data)))
	}
	done(data, nil)
}

type memStore struct{ m map[string][]byte }

func newStore() *memStore { return &memStore{m: map[string][]byte{}} }
func (s *memStore) Get(k string, done func([]byte, bool)) {
	d, ok := s.m[k]
	done(d, ok)
}
func (s *memStore) Put(k string, d []byte, done func(error)) { s.m[k] = d; done(nil) }
func (s *memStore) Delete(k string, done func(error))        { delete(s.m, k); done(nil) }
func (s *memStore) Keys(done func([]string)) {
	var ks []string
	for k := range s.m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	done(ks)
}

type fakeRuntime struct {
	has      bool
	texts    []string
	rates    []float64
	pending  []func(Audio, error)
	cancels  int
	models   [][]byte
	finishAt func(text string) (Audio, error)
}

func (r *fakeRuntime) Available() bool { return r.has }
func (r *fakeRuntime) Synthesize(model, cfg []byte, text string, rate float64, done func(Audio, error)) func() {
	r.texts, r.rates, r.models = append(r.texts, text), append(r.rates, rate), append(r.models, model)
	if r.finishAt != nil {
		a, err := r.finishAt(text)
		done(a, err)
		return func() {}
	}
	r.pending = append(r.pending, done)
	return func() { r.cancels++ }
}

type fakeOutput struct {
	played  []Audio
	onTime  func(time.Duration)
	onEnd   func()
	stopped bool
	paused  bool
}

func (o *fakeOutput) Play(a Audio, onTime func(time.Duration), onEnd func()) Playback {
	o.played, o.onTime, o.onEnd = append(o.played, a), onTime, onEnd
	return o
}
func (o *fakeOutput) Stop()   { o.stopped = true }
func (o *fakeOutput) Pause()  { o.paused = true }
func (o *fakeOutput) Resume() { o.paused = false }

func sum(b []byte) string { h := md5.Sum(b); return hex.EncodeToString(h[:]) } //nolint:gosec

func voicesJSON(model, cfg []byte) []byte {
	return []byte(fmt.Sprintf(`{
 "de_DE-thorsten-medium": {"key":"de_DE-thorsten-medium","name":"thorsten","language":{"code":"de_DE","family":"de"},"quality":"medium","num_speakers":1,
  "files":{"de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx":{"size_bytes":%d,"md5_digest":"%s"},
           "de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx.json":{"size_bytes":%d,"md5_digest":"%s"},
           "de/de_DE/thorsten/medium/MODEL_CARD":{"size_bytes":10,"md5_digest":"x"}}},
 "de_DE-eva_k-x_low": {"key":"de_DE-eva_k-x_low","name":"eva_k","language":{"code":"de_DE"},"quality":"x_low","files":{"de/de_DE/eva_k/x_low/de_DE-eva_k-x_low.onnx":{"size_bytes":5},"de/de_DE/eva_k/x_low/de_DE-eva_k-x_low.onnx.json":{"size_bytes":2}}},
 "en_GB-alan-low": {"key":"en_GB-alan-low","name":"alan","language":{"code":"en_GB"},"quality":"low","files":{"en/en_GB/alan/low/en_GB-alan-low.onnx":{"size_bytes":5}}},
 "fr_FR-x-high": {"name":"x","language":{"code":"fr_FR"},"quality":"high","files":{}}
}`, len(model), sum(model), len(cfg), sum(cfg)))
}

const base = "https://voices.example/"

func setup(t *testing.T) (*Engine, *fakeFetcher, *memStore, *fakeRuntime, *fakeOutput, []byte, []byte) {
	t.Helper()
	model, cfg := []byte("MODEL-BYTES"), []byte(`{"audio":{"sample_rate":22050}}`)
	f := &fakeFetcher{files: map[string][]byte{
		base + "voices.json": voicesJSON(model, cfg),
		base + "de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx":      model,
		base + "de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx.json": cfg,
	}, fail: map[string]error{}}
	st, rt, out := newStore(), &fakeRuntime{has: true}, &fakeOutput{}
	e := New(Config{Catalogue: base + "voices.json", Base: base}, f, st, rt, out)
	var loadErr error
	e.LoadCatalogue(context.Background(), func(err error) { loadErr = err })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return e, f, st, rt, out, model, cfg
}

func TestCatalogueListsVoicesOfALanguageWithSizeAndCard(t *testing.T) {
	e, _, _, _, _, model, cfg := setup(t)
	list := e.Catalogue("de-AT")
	if len(list) != 2 || list[0].Quality != "medium" || list[1].Quality != "x_low" {
		t.Fatalf("german voices, better first: %+v", list)
	}
	if list[0].Size != int64(len(model)+len(cfg)) || list[0].Licence != base+"de/de_DE/thorsten/medium/MODEL_CARD" || list[0].Name != "Thorsten" {
		t.Fatalf("size and card: %+v", list[0])
	}
	if len(e.Catalogue("en")) != 0 || len(e.Catalogue("fr")) != 0 {
		t.Fatal("a voice without both files is not offered")
	}
	if _, err := ParseCatalogue([]byte("not json")); err == nil {
		t.Fatal("bad catalogue")
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig([]byte(`{"catalogue":"https://mirror.example/v/voices.json","runtime":"piper-runtime.js"}`))
	if err != nil || c.Catalogue != "https://mirror.example/v/voices.json" || c.Base != "https://mirror.example/v/" || c.Runtime != "piper-runtime.js" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, _ := ParseConfig([]byte(`{}`)); c != DefaultConfig() {
		t.Fatalf("defaults %+v", c)
	}
	for _, bad := range []string{`{"catalogue":"http://x/y.json"}`, `{"base":"ftp://x/"}`, `{"runtime":"//evil.example/x.js"}`, `{"runtime":"javascript:alert(1)"}`, `nope`} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestInstallFetchesChecksAndKeepsTheVoiceThenRemoveDeletesIt(t *testing.T) {
	e, f, st, _, _, model, cfg := setup(t)
	var last, total int64
	var done error
	finished := false
	e.Install(context.Background(), "de_DE-thorsten-medium", func(d, tt int64) { last, total = d, tt }, func(err error) { done, finished = err, true })
	if !finished || done != nil || !e.Installed("de_DE-thorsten-medium") {
		t.Fatalf("install: finished %v err %v", finished, done)
	}
	if total != int64(len(model)+len(cfg)) || last != total {
		t.Fatalf("progress %d of %d", last, total)
	}
	if string(st.m["de_DE-thorsten-medium.onnx"]) != string(model) || len(st.m) != 2 {
		t.Fatalf("kept: %v", st.m)
	}
	if len(f.asked) != 3 || !strings.HasSuffix(f.asked[1], ".onnx.json") {
		t.Fatalf("asked %v", f.asked)
	}
	if got := e.Voices(); len(got) != 1 || got[0].Key() != "de_DE-thorsten-medium" || got[0].Lang != "de-DE" || !got[0].Local {
		t.Fatalf("voices %+v", got)
	}
	if err := e.Remove("de_DE-thorsten-medium"); err != nil || e.Installed("de_DE-thorsten-medium") || len(st.m) != 0 {
		t.Fatalf("remove: %v %v", err, st.m)
	}
}

func TestInstallRefusesAWrongFileAndAnUnknownVoice(t *testing.T) {
	e, f, st, _, _, _, _ := setup(t)
	f.files[base+"de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx"] = []byte("MODEL-BYTEX") // same size, other sum
	var got error
	e.Install(context.Background(), "de_DE-thorsten-medium", nil, func(err error) { got = err })
	if !errors.Is(got, ErrBadDownload) || len(st.m) != 0 || e.Installed("de_DE-thorsten-medium") {
		t.Fatalf("a download that is not the file: %v %v", got, st.m)
	}
	e.Install(context.Background(), "nope", nil, func(err error) { got = err })
	if !errors.Is(got, ErrUnknownVoice) {
		t.Fatalf("unknown: %v", got)
	}
	f.fail[base+"de/de_DE/thorsten/medium/de_DE-thorsten-medium.onnx.json"] = errors.New("offline")
	e.Install(context.Background(), "de_DE-thorsten-medium", nil, func(err error) { got = err })
	if got == nil || len(st.m) != 0 {
		t.Fatalf("offline: %v", got)
	}
}

func installed(t *testing.T) (*Engine, *fakeRuntime, *fakeOutput) {
	e, _, _, rt, out, _, _ := setup(t)
	e.Install(context.Background(), "de_DE-thorsten-medium", nil, func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	})
	return e, rt, out
}

func TestSpeakSynthesizesPlaysAndMarksWordsByHowFarTheSoundHasGot(t *testing.T) {
	e, rt, out := installed(t)
	var kinds []string
	var words [][2]int
	ended := 0
	e.Speak(read.Utterance{Text: "Hallo schöne Welt", Lang: "de", Rate: 1.5},
		read.Handler{OnStart: func(k string) { kinds = append(kinds, k) }, OnWord: func(i, n int) { words = append(words, [2]int{i, n}) }, OnEnd: func() { ended++ }})
	if len(rt.texts) != 1 || rt.texts[0] != "Hallo schöne Welt" || rt.rates[0] != 1.5 || string(rt.models[0]) != "MODEL-BYTES" {
		t.Fatalf("runtime asked: %+v", rt.texts)
	}
	rt.pending[0](Audio{Samples: make([]float32, 22050*2), SampleRate: 22050}, nil) // two seconds
	if len(kinds) != 1 || kinds[0] != read.KindDownloaded || len(out.played) != 1 {
		t.Fatalf("started: %v %d", kinds, len(out.played))
	}
	out.onTime(0)
	out.onTime(1100 * time.Millisecond)
	out.onTime(1999 * time.Millisecond)
	want := [][2]int{{0, 5}, {6, 6}, {13, 4}}
	if fmt.Sprint(words) != fmt.Sprint(want) {
		t.Fatalf("words %v, want %v", words, want)
	}
	out.onEnd()
	if ended != 1 {
		t.Fatalf("ended %d", ended)
	}
}

func TestSpeakCancelPausesAndErrors(t *testing.T) {
	e, rt, out := installed(t)
	ended, errs := 0, 0
	h := read.Handler{OnEnd: func() { ended++ }, OnError: func(error) { errs++ }}
	sp := e.Speak(read.Utterance{Text: "Eins", Lang: "de"}, h)
	sp.Cancel() // while the sound is still being made
	if rt.cancels != 1 {
		t.Fatalf("synthesis cancelled: %d", rt.cancels)
	}
	rt.pending[0](Audio{Samples: []float32{0.1}, SampleRate: 10}, nil)
	if len(out.played) != 0 || ended != 0 {
		t.Fatal("nothing plays after cancel")
	}
	sp = e.Speak(read.Utterance{Text: "Zwei", Lang: "de"}, h)
	rt.pending[1](Audio{Samples: make([]float32, 100), SampleRate: 100}, nil)
	sp.Pause()
	if !out.paused {
		t.Fatal("pause")
	}
	sp.Resume()
	sp.Cancel()
	out.onEnd()
	if !out.stopped || ended != 0 {
		t.Fatal("cancel stops the sound and the end is not reported")
	}
	e.Speak(read.Utterance{Text: "Drei", Lang: "de"}, h)
	rt.pending[2](Audio{}, nil)
	if errs != 1 {
		t.Fatalf("a runtime that makes no sound is an error: %d", errs)
	}
}

func TestSpeakWithoutARuntimeOrTheVoice(t *testing.T) {
	e, rt, _ := installed(t)
	var got error
	h := read.Handler{OnError: func(err error) { got = err }}
	e.Speak(read.Utterance{Text: "x", Voice: "en_GB-alan-low"}, h)
	if !errors.Is(got, ErrNotInstalled) {
		t.Fatalf("not installed: %v", got)
	}
	rt.has = false
	e.Speak(read.Utterance{Text: "x", Lang: "de"}, h)
	if !errors.Is(got, ErrNoRuntime) || e.Available() {
		t.Fatalf("no runtime: %v", got)
	}
}

func TestRefreshFindsWhatIsAlreadyKept(t *testing.T) {
	st := newStore()
	st.m["de_DE-thorsten-medium.onnx"], st.m["de_DE-thorsten-medium.onnx.json"], st.m["lonely.onnx"] = []byte("a"), []byte("b"), []byte("c")
	e := New(DefaultConfig(), &fakeFetcher{}, st, &fakeRuntime{has: true}, &fakeOutput{})
	e.Refresh(nil)
	got := e.Voices()
	if len(got) != 1 || got[0].Key() != "de_DE-thorsten-medium" || got[0].Lang != "de-DE" {
		t.Fatalf("a voice needs both files: %+v", got)
	}
}
