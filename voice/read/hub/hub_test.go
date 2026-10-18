package hub

import (
	"net/url"
	"strings"
	"testing"
)

func TestTheHubHasFourVoicesWithTheirTermsAndPinnedFiles(t *testing.T) {
	l, err := Load()
	if err != nil || len(l.Voices) != 4 || l.Read == "" {
		t.Fatalf("%+v %v", l, err)
	}
	seen := map[string]bool{}
	for _, v := range l.Voices {
		seen[v.Lang+"/"+v.Gender] = true
		switch {
		case v.Licence == "" || v.LicenceURL == "" || v.Card == "" || v.Dataset == "":
			t.Errorf("%s: terms are missing: %+v", v.ID, v)
		case v.ModelSize <= 0 || len(v.ModelMD5) != 32 || v.ConfigSize <= 0 || len(v.ConfigMD5) != 32:
			t.Errorf("%s: files are not pinned", v.ID)
		case v.Source != ID || !strings.HasPrefix(v.Base, "https://"):
			t.Errorf("%s: source or base: %q %q", v.ID, v.Source, v.Base)
		}
		for _, a := range []string{v.LicenceURL, v.Dataset, v.Base} {
			if u, err := url.Parse(a); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				t.Errorf("%s: address %q", v.ID, a)
			}
		}
		if !strings.HasPrefix(v.Model, strings.Replace(strings.SplitN(v.ID, "-", 2)[0], "_", "/", 1)[:2]) {
			t.Errorf("%s: model path %q", v.ID, v.Model)
		}
	}
	for _, want := range []string{"en-US/male", "en-US/female", "en-GB/male", "en-GB/female"} {
		if !seen[want] {
			t.Errorf("no %s voice", want)
		}
	}
	if len(Voices()) != 4 {
		t.Fatal("Voices")
	}
}
