package imagerepo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func fake(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/images/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.HasSuffix(r.URL.Path, "/thumb/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG-small"))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"a1","title":"Harbour","creator":"Ana","license":"by","license_version":"4.0","license_url":"https://cc/by","foreign_landing_url":"https://x/a1","width":800,"height":600},{"id":"","title":"skip"}]}`))
	})
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"query":{"pages":{
"1":{"index":2,"title":"File:Boat.jpg","imageinfo":[{"thumburl":"` + "http://" + r.Host + `/boat.jpg","url":"u","descriptionurl":"https://c/Boat","mime":"image/jpeg","width":900,"height":700,"extmetadata":{"LicenseShortName":{"value":"CC BY-SA 3.0"},"Artist":{"value":"<a href=\"x\">Bo &amp; Co</a>"}}}]},
"2":{"index":1,"title":"File:Old.png","imageinfo":[{"url":"http://` + r.Host + `/old.png","mime":"image/png","extmetadata":{"LicenseShortName":{"value":"Public domain"}}}]},
"3":{"index":3,"title":"File:NC.jpg","imageinfo":[{"url":"u","mime":"image/jpeg","extmetadata":{"LicenseShortName":{"value":"CC BY-NC 2.0"}}}]},
"4":{"index":4,"title":"File:Fair.jpg","imageinfo":[{"url":"u","mime":"image/jpeg","extmetadata":{"LicenseShortName":{"value":"Fair use"}}}]},
"5":{"index":5,"title":"File:Doc.pdf","imageinfo":[{"url":"u","mime":"application/pdf","extmetadata":{"LicenseShortName":{"value":"CC0"}}}]}}}}`))
	})
	mux.HandleFunc("/boat.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	})
	mux.HandleFunc("/old.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s, &hits
}

func TestNothingIsContactedUntilARepositoryIsEnabled(t *testing.T) {
	s, hits := fake(t)
	l := New()
	l.Register(Openverse{Base: s.URL})
	if _, err := l.Search(context.Background(), "Openverse", "harbour", 1); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("got %v", err)
	}
	if _, _, err := l.Fetch(context.Background(), Result{Repo: "Openverse", URL: s.URL + "/boat.jpg"}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("fetch: %v", err)
	}
	if hits.Load() != 0 || l.Enable("nope", true) {
		t.Fatalf("a request left: %d", hits.Load())
	}
}

func TestOpenverseResultsCarryAuthorLicenceAndSource(t *testing.T) {
	s, _ := fake(t)
	l := New()
	l.Register(Openverse{Base: s.URL})
	l.Enable("Openverse", true)
	res, err := l.Search(context.Background(), "Openverse", "harbour", 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	r := res[0]
	if r.License != "CC BY 4.0" || r.Creator != "Ana" || r.Source != "https://x/a1" || r.W != 800 {
		t.Fatalf("%+v", r)
	}
	if got := r.Credit(); got != "Harbour by Ana, CC BY 4.0, via Openverse" {
		t.Fatalf("credit %q", got)
	}
	data, mime, err := l.Fetch(context.Background(), r)
	if err != nil || mime != "image/png" || len(data) == 0 {
		t.Fatalf("%v %q", err, mime)
	}
}

func TestCommonsKeepsOnlyReusableLicencesInRankOrder(t *testing.T) {
	s, _ := fake(t)
	l := New()
	l.Register(Commons{Base: s.URL})
	l.Enable("Wikimedia Commons", true)
	res, err := l.Search(context.Background(), "Wikimedia Commons", "boat", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Title != "Old" || res[1].Title != "Boat" {
		t.Fatalf("%+v", res)
	}
	if res[1].Creator != "Bo & Co" || res[1].License != "CC BY-SA 3.0" {
		t.Fatalf("%+v", res[1])
	}
}

func TestFetchLimitsTypeAndSize(t *testing.T) {
	s, _ := fake(t)
	l := New()
	l.Register(Commons{Base: s.URL})
	l.Enable("Wikimedia Commons", true)
	ctx := context.Background()
	if _, _, err := l.Fetch(ctx, Result{Repo: "Wikimedia Commons", URL: s.URL + "/old.png"}); !errors.Is(err, ErrBadType) {
		t.Fatalf("type: %v", err)
	}
	l.MaxBytes = 50
	if _, _, err := l.Fetch(ctx, Result{Repo: "Wikimedia Commons", URL: s.URL + "/boat.jpg"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
	l.MaxBytes = 0
	for range 3 {
		if _, _, err := l.Fetch(ctx, Result{Repo: "Wikimedia Commons", URL: s.URL + "/boat.jpg"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReusable(t *testing.T) {
	for s, want := range map[string]bool{"CC BY 4.0": true, "CC BY-SA 3.0": true, "CC0": true, "Public domain": true, "PD-old": true,
		"CC BY-NC 2.0": false, "CC BY-ND 2.0": false, "Fair use": false, "": false} {
		if reusable(s) != want {
			t.Errorf("%q", s)
		}
	}
}

func TestOperatorListRegistersMirrorsUnderTheirOwnNames(t *testing.T) {
	rs, err := ParseConfig([]byte(`[{"name":"Our Commons","kind":"mediawiki","base":"https://wiki.example.org"},{"name":"Mirror","kind":"openverse","base":"http://localhost:9"}]`))
	if err != nil || len(rs) != 2 || rs[0].Name() != "Our Commons" || rs[1].Name() != "Mirror" {
		t.Fatalf("%v %v", rs, err)
	}
	l := New()
	l.RegisterAll(rs)
	if l.Enabled("Mirror") || len(l.Names()) != 2 {
		t.Fatal("registered repositories start off")
	}
	for _, bad := range []string{`[{"name":"","kind":"openverse","base":"https://a"}]`, `[{"name":"a","kind":"x","base":"https://a"}]`,
		`[{"name":"a","kind":"openverse","base":"http://a.example"}]`, `[{"name":"a","kind":"openverse","base":"https://a"},{"name":"a","kind":"openverse","base":"https://b"}]`, `nope`} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
