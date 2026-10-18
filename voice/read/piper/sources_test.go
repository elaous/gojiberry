package piper

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func repoCatalogue(model, cfg []byte) []byte {
	return []byte(fmt.Sprintf(`{"fr_FR-demo-medium":{"key":"fr_FR-demo-medium","name":"demo","language":{"code":"fr_FR"},"quality":"medium","num_speakers":1,
 "files":{"fr/demo/medium/fr_FR-demo-medium.onnx":{"size_bytes":%d,"md5_digest":"%s"},"fr/demo/medium/fr_FR-demo-medium.onnx.json":{"size_bytes":%d,"md5_digest":"%s"},"fr/demo/medium/MODEL_CARD":{"size_bytes":3,"md5_digest":"x"}}}}`,
		len(model), sum(model), len(cfg), sum(cfg)))
}

func TestARepositoryTheLastOnesAddedHasItsOwnBaseAndNamespace(t *testing.T) {
	model, cfg := []byte("MODEL"), []byte(`{"audio":{"sample_rate":22050}}`)
	list, err := ParseRepository("https://voices.example/pack/voices.json", "repo1", repoCatalogue(model, cfg))
	if err != nil || len(list) != 1 || list[0].ID != "repo1:fr_FR-demo-medium" || list[0].Base != "https://voices.example/pack/" || list[0].Source != "repo1" {
		t.Fatalf("%+v %v", list, err)
	}
	f := &fakeFetcher{files: map[string][]byte{
		"https://voices.example/pack/fr/demo/medium/fr_FR-demo-medium.onnx":      model,
		"https://voices.example/pack/fr/demo/medium/fr_FR-demo-medium.onnx.json": cfg,
	}}
	st := newStore()
	e := New(DefaultConfig(), f, st, &fakeRuntime{has: true}, &fakeOutput{})
	e.AddVoices(list)
	got := e.SourceVoices("repo1", "fr")
	if len(got) != 1 || got[0].Source != "repo1" || !strings.HasSuffix(got[0].Licence, "/fr/demo/medium/MODEL_CARD") || !strings.HasPrefix(got[0].Licence, "https://voices.example/pack/") {
		t.Fatalf("listing %+v", got)
	}
	if e.SourceVoices("", "fr") != nil {
		t.Fatal("the engine's own catalogue does not list another source's voices")
	}
	var installErr error
	finished := false
	e.Install(context.Background(), "repo1:fr_FR-demo-medium", nil, func(err error) { installErr, finished = err, true })
	if !finished || installErr != nil || !e.Installed("repo1:fr_FR-demo-medium") {
		t.Fatalf("install: %v", installErr)
	}
	if len(f.asked) != 2 || !strings.HasPrefix(f.asked[0], "https://voices.example/pack/") {
		t.Fatalf("asked %v", f.asked)
	}
}

func TestRepositoryAddressesAreHTTPSCatalogues(t *testing.T) {
	for _, bad := range []string{"", "http://x.example/voices.json", "https://user:pw@x.example/voices.json", "https://x.example/voices", "https://x.example/a b/voices.json", "ftp://x/voices.json", "https://x.example/voices.json#f", "//x.example/voices.json"} {
		if CheckAddress(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if CheckAddress("https://voices.example/piper/voices.json") != nil {
		t.Error("a catalogue address")
	}
	if _, err := ParseRepository("https://x.example/voices.json", "r", []byte("not json")); err == nil {
		t.Error("not a catalogue")
	}
}

func TestAVoiceFromTheOwnFilesIsKeptAsItIs(t *testing.T) {
	cfg := []byte(`{"audio":{"sample_rate":22050},"language":{"code":"en_GB"},"phoneme_id_map":{"a":[1]}}`)
	st := newStore()
	e := New(DefaultConfig(), &fakeFetcher{}, st, &fakeRuntime{has: true}, &fakeOutput{})
	var id string
	var err error
	e.InstallFiles(context.Background(), "My dog's voice!", cfg, []byte("MODEL"), func(i string, e error) { id, err = i, e })
	if err != nil || id != "en_GB-my_dog_s_voice-own" || !e.Installed(id) || string(st.m[id+".onnx"]) != "MODEL" || string(st.m[id+".onnx.json"]) != string(cfg) {
		t.Fatalf("%q %v %v", id, err, st.m)
	}
	vs := e.Voices()
	if len(vs) != 1 || vs[0].Lang != "en-GB" {
		t.Fatalf("listed under its language: %+v", vs)
	}
	for name, c := range map[string][]byte{"not json": []byte("x"), "no rate": []byte(`{"phoneme_id_map":{"a":[1]}}`), "no map": []byte(`{"audio":{"sample_rate":22050}}`)} {
		var gotErr error
		e.InstallFiles(context.Background(), "x", c, []byte("M"), func(_ string, err error) { gotErr = err })
		if gotErr != ErrBadVoiceFiles {
			t.Errorf("%s: %v", name, gotErr)
		}
	}
	var gotErr error
	e.InstallFiles(context.Background(), "x", cfg, nil, func(_ string, err error) { gotErr = err })
	if gotErr != ErrBadVoiceFiles {
		t.Errorf("no model: %v", gotErr)
	}
}

func TestStarListsEveryLanguage(t *testing.T) {
	all := []CatalogueVoice{{ID: "fr_FR-b", Lang: "fr-FR"}, {ID: "en_GB-a", Lang: "en-GB"}}
	got := ForLanguage(all, "*")
	if len(got) != 2 || got[0].ID != "en_GB-a" {
		t.Fatalf("%+v", got)
	}
}
