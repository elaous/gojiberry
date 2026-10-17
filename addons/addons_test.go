package addons

import "testing"

func TestKnownNamesAreUniqueAndFindable(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Known {
		if s.Name == "" || s.Method == "" || s.Purpose == "" || seen[s.Name] {
			t.Errorf("bad spec %+v", s)
		}
		seen[s.Name] = true
		if got, ok := Find(s.Name); !ok || got != s {
			t.Errorf("find %q", s.Name)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Error("an unknown name is not found")
	}
}

func TestParseModelsKeepsOnlyUsableOnes(t *testing.T) {
	ms, err := ParseModels([]byte(`[
		{"id":"tiny.en","label":"Tiny (English)","lang":"en","size":77700000,"licence":"https://example.org/licence","installed":true},
		{"id":"base","size":-5,"licence":"MIT"},
		{"id":"bad id with spaces"},
		{"id":"../escape"},
		{"id":"base","label":"duplicate"},
		{"id":"x","size":1e30,"licence":"javascript:alert(1)"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms[0].ID != "tiny.en" || !ms[0].Installed || ms[0].Size != 77700000 || ms[0].Lang != "en" {
		t.Fatalf("%+v", ms)
	}
	if ms[1].Label != "base" || ms[1].Size != 0 || ms[1].Licence != "MIT" {
		t.Fatalf("defaults: %+v", ms[1])
	}
	if l, ok := ms[0].LicenceLink(); !ok || l != "https://example.org/licence" {
		t.Error("an https licence is a link")
	}
	if _, ok := ms[1].LicenceLink(); ok {
		t.Error("a short name is text")
	}
	if _, ok := ms[2].LicenceLink(); ok || ms[2].Size != 0 {
		t.Errorf("a script address is not a link, a huge size is not believed: %+v", ms[2])
	}
	if _, err := ParseModels([]byte("nope")); err == nil {
		t.Error("not JSON")
	}
}

func TestSize(t *testing.T) {
	for in, want := range map[int64]string{0: "", -1: "", 1 << 20: "1.0 MB", 77700000: "74.1 MB", 3 << 30: "3.0 GB"} {
		if got := Size(in); got != want {
			t.Errorf("Size(%d) = %q, want %q", in, got, want)
		}
	}
}
