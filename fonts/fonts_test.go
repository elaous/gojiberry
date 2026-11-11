package fonts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var bg = context.Background()

func TestStaticMarksTheSource(t *testing.T) {
	r := Static("Mine", Font{Family: "A"}, Font{Family: "B", Source: "ignored"})
	got, err := r.Fonts(bg)
	if err != nil || r.Name() != "Mine" || len(got) != 2 || got[0].Source != "Mine" || got[1].Source != "Mine" {
		t.Fatalf("%v %v", got, err)
	}
	got[0].Family = "changed"
	again, _ := r.Fonts(bg)
	if again[0].Family != "A" {
		t.Fatal("callers must not be able to modify the repository's list")
	}
}

func TestWebSafe(t *testing.T) {
	fonts, err := WebSafe().Fonts(bg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Arial", "Georgia", "Courier New", "monospace", "system-ui"} {
		if _, ok := Find(fonts, want); !ok {
			t.Errorf("web-safe set lacks %s", want)
		}
	}
	if f, _ := Find(fonts, "georgia"); f.Category != "serif" || f.Source != "Web safe" || len(f.Variants) != 4 {
		t.Fatalf("georgia = %+v (Find is case-insensitive)", f)
	}
}

type failing struct{ name string }

func (f failing) Name() string                          { return f.name }
func (f failing) Fonts(context.Context) ([]Font, error) { return nil, errors.New("boom") }

func TestMultiMergesFamiliesAndKeepsWhatWorks(t *testing.T) {
	a := Static("A", Font{Family: "Roboto", Category: "", Variants: []Variant{{Weight: 400}, {Weight: 700}}}, Font{Family: "Zed"})
	b := Static("B", Font{Family: "roboto", Category: "sans-serif", Variants: []Variant{{Weight: 400}, {Weight: 300, Italic: true}, {Weight: 300}}}, Font{Family: "Alpha"})
	m := Multi(a, failing{"C"}, b)
	got, err := m.Fonts(bg)
	if err == nil || !strings.Contains(err.Error(), "C: boom") {
		t.Fatalf("err = %v", err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Family)
	}
	if !reflect.DeepEqual(names, []string{"Alpha", "Roboto", "Zed"}) {
		t.Fatalf("names = %v (sorted, one Roboto)", names)
	}
	rob, _ := Find(got, "Roboto")
	if rob.Source != "A" || rob.Category != "sans-serif" {
		t.Fatalf("roboto = %+v: first source wins, category is filled in from the second", rob)
	}
	want := []Variant{{Weight: 300}, {Weight: 300, Italic: true}, {Weight: 400}, {Weight: 700}}
	if !reflect.DeepEqual(rob.Variants, want) {
		t.Fatalf("variants = %+v", rob.Variants)
	}
	if m.Name() != "A + C + B" {
		t.Fatalf("name = %q", m.Name())
	}
}

type counting struct {
	calls int
	err   error
}

func (c *counting) Name() string { return "counting" }
func (c *counting) Fonts(context.Context) ([]Font, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return []Font{{Family: "X"}}, nil
}

func TestCachedServesStaleOnFailure(t *testing.T) {
	src := &counting{}
	r := Cached(src, time.Hour)
	if r.Name() != "counting" {
		t.Fatal("name")
	}
	_, _ = r.Fonts(bg)
	_, _ = r.Fonts(bg)
	if src.calls != 1 {
		t.Fatalf("calls = %d, want the second answered from the cache", src.calls)
	}
	r.(*cached).fetched = time.Now().Add(-2 * time.Hour)
	src.err = errors.New("down")
	got, err := r.Fonts(bg)
	if err == nil || len(got) != 1 || got[0].Family != "X" {
		t.Fatalf("got %v, %v: the stale list is served with the error", got, err)
	}
	fresh := Cached(&counting{err: errors.New("down")}, time.Hour)
	if got, err := fresh.Fonts(bg); err == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSearch(t *testing.T) {
	fonts := []Font{{Family: "Open Sans"}, {Family: "Sans Forgetica"}, {Family: "Noto Sans Mono"}, {Family: "Lora"}}
	names := func(fs []Font) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Family)
		}
		return out
	}
	if got := names(Search(fonts, "sans")); !reflect.DeepEqual(got, []string{"Sans Forgetica", "Open Sans", "Noto Sans Mono"}) {
		t.Fatalf("prefix matches come first: %v", got)
	}
	if got := names(Search(fonts, "  SANS  mono ")); !reflect.DeepEqual(got, []string{"Noto Sans Mono"}) {
		t.Fatalf("every word must match: %v", got)
	}
	if got := Search(fonts, ""); len(got) != 4 {
		t.Fatal("an empty query matches everything")
	}
	if got := Search(fonts, "zzz"); len(got) != 0 {
		t.Fatal("no match")
	}
}

func TestSourcesAndFromSource(t *testing.T) {
	fonts := []Font{{Family: "a", Source: "X"}, {Family: "b", Source: "Y"}, {Family: "c", Source: "X"}, {Family: "d"}}
	if got := Sources(fonts); !reflect.DeepEqual(got, []string{"X", "Y"}) {
		t.Fatalf("sources = %v", got)
	}
	if len(FromSource(fonts, "X")) != 2 || len(FromSource(fonts, "")) != 4 || len(FromSource(fonts, "Q")) != 0 {
		t.Fatal("FromSource")
	}
}

func TestCSSFamily(t *testing.T) {
	for _, tt := range []struct {
		f    Font
		want string
	}{
		{Font{Family: "Open Sans", Category: "sans-serif"}, `"Open Sans", sans-serif`},
		{Font{Family: "Lora", Category: "serif"}, `"Lora", serif`},
		{Font{Family: "Pacifico", Category: "handwriting"}, `"Pacifico", cursive`},
		{Font{Family: "Anton", Category: "display"}, `"Anton", fantasy`},
		{Font{Family: "Plain"}, `"Plain"`},
		{Font{Family: "serif", Category: "serif"}, `serif`},
		{Font{Family: "monospace"}, `monospace`},
		{Font{Family: `Odd"Name\`}, `"Odd\"Name\\"`},
	} {
		if got := CSSFamily(tt.f); got != tt.want {
			t.Errorf("CSSFamily(%+v) = %s, want %s", tt.f, got, tt.want)
		}
	}
}

func TestFontFaceCSS(t *testing.T) {
	css := FontFaceCSS([]Font{
		{Family: "Mine", Variants: []Variant{{Weight: 400, URL: "/f/a.ttf"}, {Weight: 700, Italic: true, URL: "/f/b.ttf"}, {Weight: 300}}},
		{Family: "Installed", Variants: []Variant{{Weight: 400}}},
	})
	for _, want := range []string{
		`@font-face{font-family:"Mine";font-weight:400;font-style:normal;font-display:swap;src:url("/f/a.ttf")}`,
		`font-weight:700;font-style:italic`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in\n%s", want, css)
		}
	}
	if strings.Count(css, "@font-face") != 2 {
		t.Fatalf("only variants with a URL get a rule:\n%s", css)
	}
}

func TestClosest(t *testing.T) {
	f := Font{Variants: []Variant{{Weight: 300}, {Weight: 400}, {Weight: 700}, {Weight: 700, Italic: true}}}
	for _, tt := range []struct {
		w      int
		italic bool
		want   Variant
	}{
		{400, false, Variant{Weight: 400}}, {600, false, Variant{Weight: 700}}, {350, false, Variant{Weight: 300}},
		{400, true, Variant{Weight: 700, Italic: true}}, {900, false, Variant{Weight: 700}},
	} {
		if got, ok := f.Closest(tt.w, tt.italic); !ok || got != tt.want {
			t.Errorf("Closest(%d,%v) = %+v, want %+v", tt.w, tt.italic, got, tt.want)
		}
	}
	if _, ok := (Font{}).Closest(400, false); ok {
		t.Fatal("no variants")
	}
}

func TestParseVariantKey(t *testing.T) {
	for _, tt := range []struct {
		k      string
		w      int
		italic bool
		ok     bool
	}{
		{"regular", 400, false, true}, {"italic", 400, true, true}, {"700", 700, false, true},
		{"300italic", 300, true, true}, {"garbage", 0, false, false}, {"0", 0, false, false}, {"5000", 0, false, false},
	} {
		w, italic, ok := parseVariantKey(tt.k)
		if w != tt.w || italic != tt.italic || ok != tt.ok {
			t.Errorf("parseVariantKey(%q) = %d,%v,%v", tt.k, w, italic, ok)
		}
	}
}

func TestEscapePath(t *testing.T) {
	if got := escapePath("my fonts/A B.ttf"); got != "my%20fonts/A%20B.ttf" {
		t.Fatalf("got %q", got)
	}
}

func TestMultiFillsInAURLAnEarlierSourceLacked(t *testing.T) {
	first := Static("first", Font{Family: "Lato", Variants: []Variant{{Weight: 400}}})
	second := Static("second", Font{Family: "Lato", Variants: []Variant{{Weight: 400, URL: "https://f/lato.woff2"}}})
	fonts, err := Multi(first, second).Fonts(context.Background())
	if err != nil || len(fonts) != 1 || len(fonts[0].Variants) != 1 || fonts[0].Variants[0].URL != "https://f/lato.woff2" {
		t.Fatalf("%+v %v", fonts, err)
	}
}
