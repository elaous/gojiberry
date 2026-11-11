package fontserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCSSFontFacesExtractsWeightStyleAndURL(t *testing.T) {
	css := `@font-face {
  font-family: 'Open Sans';
  font-style: italic;
  font-weight: 400;
  font-stretch: normal;
  src: url(https://fonts.gstatic.com/s/opensans/v44/a.ttf) format('truetype');
}
@font-face {
  font-family: 'Open Sans';
  font-style: italic;
  font-weight: 700;
  font-stretch: normal;
  src: url(https://fonts.gstatic.com/s/opensans/v44/b.ttf) format('truetype');
}
@font-face {
  font-family: 'Open Sans';
  font-style: normal;
  font-weight: 400;
  font-stretch: normal;
  src: url(https://fonts.gstatic.com/s/opensans/v44/c.ttf) format('truetype');
}
@font-face {
  font-family: 'Open Sans';
  font-style: normal;
  font-weight: 700;
  font-stretch: normal;
  src: url(https://fonts.gstatic.com/s/opensans/v44/d.ttf) format('truetype');
}`
	got := parseCSSFontFaces(css)
	want := []fontFaceEntry{
		{Weight: 400, Italic: true, URL: "https://fonts.gstatic.com/s/opensans/v44/a.ttf"},
		{Weight: 700, Italic: true, URL: "https://fonts.gstatic.com/s/opensans/v44/b.ttf"},
		{Weight: 400, Italic: false, URL: "https://fonts.gstatic.com/s/opensans/v44/c.ttf"},
		{Weight: 700, Italic: false, URL: "https://fonts.gstatic.com/s/opensans/v44/d.ttf"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseCSSFontFaces returned %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseCSSFontFacesEmptyForUnrecognizedFamily(t *testing.T) {
	if got := parseCSSFontFaces(""); len(got) != 0 {
		t.Fatalf("parseCSSFontFaces(\"\") = %+v, want empty", got)
	}
}

func TestParseCSSFontFacesSkipsBlocksWithNoTrueTypeFormat(t *testing.T) {
	css := `@font-face {
  font-family: 'Roboto';
  font-style: normal;
  font-weight: 400;
  font-stretch: normal;
  src: url(https://fonts.gstatic.com/l/font?kit=abc);
}`
	if got := parseCSSFontFaces(css); len(got) != 0 {
		t.Fatalf("parseCSSFontFaces = %+v, want empty (no format('truetype') annotation)", got)
	}
}

func TestDownloadSlugNormalizesFamilyNames(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Open Sans", "open-sans"},
		{"open sans", "open-sans"},
		{"  Noto Sans JP  ", "noto-sans-jp"},
		{"M PLUS 1p", "m-plus-1p"},
		{"IBM Plex Mono", "ibm-plex-mono"},
	}
	for _, tt := range tests {
		if got := downloadSlug(tt.in); got != tt.want {
			t.Errorf("downloadSlug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLocalDownloadFetchesWritesAndRescans(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, Config{FontsDir: dir})
	var gotFamily string
	srv.fetchGoogleFontFiles = func(family string) ([]googleFontFile, error) {
		gotFamily = family
		return []googleFontFile{
			{Weight: 400, Italic: false, Data: buildTestSFNT(t, []nameRecordSpec{
				{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Test Family"},
				{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Regular"},
			})},
			{Weight: 700, Italic: false, Data: buildTestSFNT(t, []nameRecordSpec{
				{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Test Family"},
				{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Bold"},
			})},
		}, nil
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/download?family=Test+Family", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /local/download = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if gotFamily != "Test Family" {
		t.Fatalf("fetchGoogleFontFiles called with family=%q, want \"Test Family\"", gotFamily)
	}

	var body struct {
		Families   []Family `json:"families"`
		Downloaded int      `json:"downloaded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Downloaded != 2 {
		t.Fatalf("downloaded = %d, want 2", body.Downloaded)
	}
	if len(body.Families) != 1 || body.Families[0].Name != "Test Family" || len(body.Families[0].Variants) != 2 {
		t.Fatalf("families = %+v, want one \"Test Family\" with 2 variants", body.Families)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "google-cache", "test-family"))
	if err != nil {
		t.Fatalf("google-cache/test-family: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("google-cache/test-family has %d files, want 2: %+v", len(entries), entries)
	}

	relPath := body.Families[0].Variants[0].Path
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/files/"+relPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /local/files/%s = %d, want 200", relPath, rec.Code)
	}
}

func TestLocalDownloadIsIdempotentOnRepeatedCalls(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, Config{FontsDir: dir})
	srv.fetchGoogleFontFiles = func(family string) ([]googleFontFile, error) {
		return []googleFontFile{{Weight: 400, Data: buildTestSFNT(t, []nameRecordSpec{
			{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Repeat Family"},
			{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Regular"},
		})}}, nil
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/download?family=Repeat+Family", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: POST /local/download = %d, want 200", i, rec.Code)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "google-cache", "repeat-family"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("re-downloading the same family produced %d files, want 1 (overwritten, not duplicated)", len(entries))
	}
}

func TestLocalDownloadMissingFamilyIs400(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/download", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /local/download (no family) = %d, want 400", rec.Code)
	}
}

func TestLocalDownloadInvalidFamilyIs400(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/download?family="+"../../etc/passwd", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /local/download?family=../../etc/passwd = %d, want 400", rec.Code)
	}
}

func TestLocalDownloadUnknownGoogleFamilyIs502(t *testing.T) {
	srv := newTestServer(t, Config{})
	srv.fetchGoogleFontFiles = func(family string) ([]googleFontFile, error) {
		return nil, fmt.Errorf("fontserver: no such Google Fonts family %q", family)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/download?family=Nonexistent+Family", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /local/download (unknown family) = %d, want 502", rec.Code)
	}
}

func TestLocalDownloadMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/download?family=Roboto", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /local/download = %d, want 405", rec.Code)
	}
}
