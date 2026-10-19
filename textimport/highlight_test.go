package textimport

import (
	"math/rand"
	"strings"
	"testing"
)

func classes(spans []span) map[string]class {
	out := map[string]class{}
	for _, s := range spans {
		if t := strings.TrimSpace(s.text); t != "" {
			out[t] = s.c
		}
	}
	return out
}

func join(spans []span) string {
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.text)
	}
	return sb.String()
}

func TestHighlightPaintsKeysStringsNumbersAndWords(t *testing.T) {
	json := classes(highlight("json", `{"name": "ana", "n": -12.5e3, "ok": true, "none": null, "list": [1, "x"]}`))
	want := map[string]class{`"name"`: key, `"ana"`: str, `-12.5e3`: num, `true`: word, `null`: word, `"list"`: key, `"x"`: str, `1`: num}
	for k, c := range want {
		if json[k] != c {
			t.Errorf("json %s is class %v, want %v", k, json[k], c)
		}
	}
	j5 := classes(highlight("json5", "{ // note\n  unquoted: 'single', /* c */ n: +.5, big: Infinity }"))
	for k, c := range map[string]class{"// note": comment, "unquoted": key, "'single'": str, "/* c */": comment, "+.5": num, "Infinity": word} {
		if j5[k] != c {
			t.Errorf("json5 %s is class %v, want %v", k, j5[k], c)
		}
	}
	y := classes(highlight("yaml", "# top\n---\nname: Ana # who\nage: 42\nlive: true\nitems:\n  - one\n  - \"two\"\n  - key: 'v'\n"))
	for k, c := range map[string]class{"# top": comment, "---": quiet, "name": key, "# who": comment, "42": num, "true": word, `"two"`: str, "'v'": str, "age": key, "items": key} {
		if y[k] != c {
			t.Errorf("yaml %s is class %v, want %v", k, y[k], c)
		}
	}
	toml := classes(highlight("toml", "# c\n[server]\nhost = \"a\"\nport = 8080 # p\ndebug = false\nwhen = 2026-10-18T10:00:00Z\n[[items]]\n"))
	for k, c := range map[string]class{"# c": comment, "[server]": section, "host": key, `"a"`: str, "8080": num, "# p": comment, "false": word, "2026-10-18T10:00:00Z": word, "[[items]]": section} {
		if toml[k] != c {
			t.Errorf("toml %s is class %v, want %v", k, toml[k], c)
		}
	}
	ini := classes(highlight("ini", "; c\n[core]\nname = x\nport: 80\nflag = yes\n"))
	for k, c := range map[string]class{"; c": comment, "[core]": section, "name": key, "port": key, "80": num, "yes": word} {
		if ini[k] != c {
			t.Errorf("ini %s is class %v, want %v", k, ini[k], c)
		}
	}
	log := classes(highlight("log", "2026-10-18 10:00:01,123 ERROR boom\n[2026-10-18T10:00:02Z] WARN slow\nOct 18 10:00:03 INFO ok\n10:00:04 DEBUG d\n"))
	for k, c := range map[string]class{"ERROR": bad, "WARN": warn, "INFO": good, "DEBUG": quiet, "2026-10-18 10:00:01,123": quiet, "[2026-10-18T10:00:02Z]": quiet} {
		if log[k] != c {
			t.Errorf("log %s is class %v, want %v", k, log[k], c)
		}
	}
}

// Painting never changes the text: the pieces add up to the file, whatever it is.
func TestHighlightKeepsEveryCharacter(t *testing.T) {
	langs := []string{"json", "json5", "yaml", "toml", "ini", "log", "", "go"}
	alphabet := []rune("{}[]:,\"'#;=-+. \n\t\f\u00a0\\/*aZ09éあ_$&!~<>@")
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 3000; n++ {
		rs := make([]rune, rng.Intn(60))
		for i := range rs {
			rs[i] = alphabet[rng.Intn(len(alphabet))]
		}
		src := string(rs)
		for _, l := range langs {
			if got := join(highlight(l, src)); got != src {
				t.Fatalf("%s changed %q into %q", l, src, got)
			}
		}
	}
	for _, src := range []string{"", "\n", "\"unterminated", "/* open", "key:", "[", "[[", "a =", "2026-"} {
		for _, l := range langs {
			if got := join(highlight(l, src)); got != src {
				t.Errorf("%s changed %q into %q", l, src, got)
			}
		}
	}
}

func TestAnImportedFileIsPaintedAndStillTheSameText(t *testing.T) {
	src := "name: Ana\nage: 42\n"
	d, err := Import(YAML, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var text string
	coloured := 0
	for _, it := range d.Content {
		if it.Chars != nil {
			text += it.Chars.Text
			if it.Chars.Format.Color != "" {
				coloured++
			}
		}
	}
	if text != src || coloured < 2 {
		t.Errorf("text %q, %d coloured pieces", text, coloured)
	}
	// A file over the limit is shown in one colour (the text is the same).
	big := strings.Repeat("a: 1\n", MaxPaint/5+10)
	d, _ = Import(YAML, []byte(big))
	pieces := 0
	for _, it := range d.Content {
		if it.Chars != nil {
			pieces++
		}
	}
	if pieces != 1 {
		t.Errorf("a large file is %d pieces", pieces)
	}
}
