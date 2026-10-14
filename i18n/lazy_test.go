package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The packs the showcase fetches on demand (static/lang-*.json) are as complete as the shipped ones: every
// message there, the same verbs in the same order, and a text for every plural category the language uses.
func TestLazyPacksAreComplete(t *testing.T) {
	files, _ := filepath.Glob("../examples/canvas-editor-showcase/static/lang-*.json")
	if len(files) < 4 {
		t.Fatalf("lazy packs: %v", files)
	}
	ref := Default().msgs["es"]
	verbs := regexp.MustCompile(`%[-+# 0]*[0-9.]*[a-zA-Z%]`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var all map[string]map[string]string
		if err := json.Unmarshal(b, &all); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(all) != 1 {
			t.Fatalf("%s holds %d languages, want 1", f, len(all))
		}
		for lang, msgs := range all {
			if want := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "lang-"), ".json"); lang != want {
				t.Errorf("%s holds %q", f, lang)
			}
			// A variant of English ("en-x-plain", plain words) only says what it says differently: the rest falls
			// back to English. Its messages must be ones that exist and keep their verbs.
			overlay := strings.HasPrefix(lang, "en-")
			for id, es := range ref {
				got, ok := msgs[id]
				if !ok {
					if !overlay {
						t.Errorf("%s lacks %q", lang, id)
					}
					continue
				}
				if a, b := verbs.FindAllString(got, -1), verbs.FindAllString(es, -1); strings.Join(a, " ") != strings.Join(b, " ") {
					t.Errorf("%s %q has the verbs %v, want %v", lang, id, a, b)
				}
			}
			if overlay {
				for id := range msgs {
					if _, ok := ref[id]; !ok {
						t.Errorf("%s has %q, which the shipped packs lack", lang, id)
					}
				}
				continue
			}
			// the plural categories the language uses, for every plural message
			for id := range ref {
				base, ok := strings.CutSuffix(id, "#other")
				if !ok {
					continue
				}
				for n := 0; n <= 200; n++ {
					cat := PluralCategory(lang, n)
					txt, ok := msgs[base+"#"+cat]
					if !ok {
						t.Errorf("%s lacks %q (the form for %d)", lang, base+"#"+cat, n)
						break
					}
					if strings.Count(txt, "%d") < 1 {
						t.Errorf("%s %q has no number", lang, base+"#"+cat)
					}
				}
			}
		}
	}
	// the right-to-left ones are known as such
	for _, l := range []string{"ar", "he"} {
		if Direction(l) != "rtl" {
			t.Errorf("%s is not right to left", l)
		}
	}
}
