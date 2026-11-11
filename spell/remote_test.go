package spell_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/elaous/gojiberry/spell"
	"github.com/elaous/gojiberry/spell/dictserver"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

func dictFS(t *testing.T) fstest.MapFS {
	deAff := "SET UTF-8\nTRY enisrtalhdu\nCOMPOUNDFLAG Z\nCOMPOUNDMIN 3\nREP 1\nREP ae ä\nKEY qwertzuiop|asdfghjkl|yxcvbnm\n"
	deDic := "4\nhaus/Z\ntür/Z\nfähre\nschloss\n"
	return fstest.MapFS{
		"de_DE/de_DE.aff":    {Data: []byte(deAff)},
		"de_DE/de_DE.dic.gz": {Data: gz(t, deDic)},
		"de_AT/de_AT.aff":    {Data: []byte(deAff)},
		"de_AT/de_AT.dic":    {Data: []byte(deDic)},
		"fr_FR/fr_FR.aff":    {Data: []byte("SET UTF-8\n")},
		"fr_FR/fr_FR.dic":    {Data: []byte("2\nbonjour\nmerci\n")},
		"es_ES/es_ES.aff":    {Data: []byte("SET UTF-8\n")}, // no .dic: not offered
		"names.json":         {Data: []byte(`{"de_DE":"Deutsch (Deutschland)"}`)},
		"README":             {Data: []byte("not a language")},
	}
}

func hosted(t *testing.T) (*spell.Remote, *atomic.Int64, *httptest.Server) {
	t.Helper()
	srv := &dictserver.Server{FS: dictFS(t)}
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.Handle("/dict/", http.StripPrefix("/dict", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		srv.ServeHTTP(w, r)
	})))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &spell.Remote{BaseURL: ts.URL + "/dict"}, &hits, ts
}

func TestTheServerListsTheLanguagesThatHaveBothFiles(t *testing.T) {
	r, _, _ := hosted(t)
	langs, err := r.Languages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range langs {
		names = append(names, l.Lang)
	}
	if strings.Join(names, ",") != "de_AT,de_DE,fr_FR" {
		t.Errorf("%v", names)
	}
	for _, l := range langs {
		if l.Lang == "de_DE" && (l.Name != "Deutsch (Deutschland)" || l.Bytes <= 0) {
			t.Errorf("%+v", l)
		}
	}
}

func TestResolveFindsTheDictionaryForALanguageTag(t *testing.T) {
	offered := []spell.Language{{Lang: "de_AT"}, {Lang: "de_DE"}, {Lang: "fr_FR"}, {Lang: "pt_BR"}}
	for tag, want := range map[string]string{"de-AT": "de_AT", "de_de": "de_DE", "de": "de_AT", "de-CH": "de_AT", "fr": "fr_FR", "pt-PT": "pt_BR", "FR-ca": "fr_FR"} {
		if got, ok := spell.Resolve(tag, offered); !ok || got != want {
			t.Errorf("%s -> %q %v, want %q", tag, got, ok, want)
		}
	}
	for _, tag := range []string{"", "ja", "xx_YY"} {
		if got, ok := spell.Resolve(tag, offered); ok {
			t.Errorf("%q -> %q", tag, got)
		}
	}
}

func TestADictionaryIsLoadedOnceWithItsCompoundsAndSuggestions(t *testing.T) {
	r, hits, _ := hosted(t)
	ctx := context.Background()
	d, err := r.Load(ctx, "de_DE")
	if err != nil {
		t.Fatal(err)
	}
	after := hits.Load()
	if after != 2 {
		t.Errorf("one request for each file: %d", after)
	}
	if !d.Has("haustür") || !d.Has("fähre") || d.Has("schlosshaus") {
		t.Error("the dictionary was not read with its compound flag")
	}
	if got := spell.NewChecker(d).Suggest("faehre", 3); len(got) == 0 || got[0] != "fähre" {
		t.Errorf("REP ae->ä: %v", got)
	}
	again, err := r.Load(ctx, "de_DE")
	if err != nil || again != d || hits.Load() != after {
		t.Errorf("the second load asks nobody: %v %v", err, hits.Load())
	}
	r2, hits2, _ := hosted(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r2.Load(ctx, "fr_FR"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if hits2.Load() != 2 {
		t.Errorf("%d requests for 8 callers", hits2.Load())
	}
}

func TestMissingAndBadNamesAreRefusedAndFailuresAreNotKept(t *testing.T) {
	r, hits, _ := hosted(t)
	ctx := context.Background()
	for _, lang := range []string{"es_ES", "ja_JP", "../etc", "de_DE/../x", ""} {
		if _, err := r.Load(ctx, lang); !errors.Is(err, spell.ErrNoDictionary) {
			t.Errorf("%q: %v", lang, err)
		}
	}
	var fail atomic.Bool
	fail.Store(true)
	srv := &dictserver.Server{FS: dictFS(t)}
	ts := httptest.NewServer(http.StripPrefix("/dict", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		srv.ServeHTTP(w, req)
	})))
	defer ts.Close()
	rr := &spell.Remote{BaseURL: ts.URL + "/dict"}
	if _, err := rr.Load(ctx, "fr_FR"); err == nil {
		t.Fatal("a bad gateway loaded")
	}
	fail.Store(false)
	if d, err := rr.Load(ctx, "fr_FR"); err != nil || !d.Has("bonjour") {
		t.Errorf("%v", err)
	}
	_ = hits
}

func TestTheServerHandlesCompressionCachingAndStrangeRequests(t *testing.T) {
	_, _, ts := hosted(t)
	get := func(path string, hdr ...string) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	r := get("/dict/de_DE.dic", "Accept-Encoding", "gzip")
	if r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("ETag") == "" || !strings.Contains(r.Header.Get("Cache-Control"), "max-age") {
		t.Errorf("%d %v", r.StatusCode, r.Header)
	}
	plain := get("/dict/de_DE.dic")
	if plain.Header.Get("Content-Encoding") != "" || plain.Header.Get("ETag") != r.Header.Get("ETag") {
		t.Errorf("a client that does not accept gzip gets the text: %v", plain.Header)
	}
	if r := get("/dict/de_DE.dic", "If-None-Match", plain.Header.Get("ETag")); r.StatusCode != http.StatusNotModified {
		t.Errorf("%d", r.StatusCode)
	}
	for _, p := range []string{"/dict/es_ES.dic", "/dict/README", "/dict/..%2fnames.json", "/dict/de_DE.txt", "/dict/names.json", "/dict/x"} {
		if r := get(p); r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	req, _ := http.NewRequest("POST", ts.URL+"/dict/de_DE.dic", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("%d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestAFileOverTheLimitIsNotServedOrLoaded(t *testing.T) {
	big := fstest.MapFS{
		"xx_XX/xx_XX.aff": {Data: []byte("SET UTF-8\n")},
		"xx_XX/xx_XX.dic": {Data: []byte(strings.Repeat("word\n", 4000))},
	}
	srv := &dictserver.Server{FS: big, MaxBytes: 1000}
	ts := httptest.NewServer(http.StripPrefix("/dict", srv))
	defer ts.Close()
	r := &spell.Remote{BaseURL: ts.URL + "/dict"}
	if _, err := r.Load(context.Background(), "xx_XX"); err == nil {
		t.Error("a file over the server's limit was served")
	}
	small := &spell.Remote{BaseURL: ts.URL + "/dict", MaxBytes: 100}
	srv.MaxBytes = 1 << 20
	if _, err := small.Load(context.Background(), "xx_XX"); err == nil {
		t.Error("a file over the client's limit was loaded")
	}
}
