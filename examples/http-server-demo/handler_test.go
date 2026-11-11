package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestEscapesTheNameParam(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/?name="+url.QueryEscape(`<script>alert("x")</script>`), nil))
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Fatalf("the name was written as markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("want the escaped name in the heading, got %s", body)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status %d", rec.Code)
	}
}

var errClientGone = errors.New("client went away")

type failingWriter struct {
	header http.Header
	code   int
}

func (f *failingWriter) Header() http.Header { return f.header }
func (f *failingWriter) Write([]byte) (int, error) {
	if f.code == 0 {
		f.code = http.StatusOK // the implicit 200 net/http sends before the first write
	}
	return 0, errClientGone
}
func (f *failingWriter) WriteHeader(code int) {
	if f.code == 0 {
		f.code = code
	}
}

func TestRenderFailureGoesThroughHTTPError(t *testing.T) {
	w := &failingWriter{header: http.Header{}}
	handler(w, httptest.NewRequest("GET", "/?name=x", nil))
	if got := w.header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type %q: http.Error was not reached", got)
	}
	if w.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v: http.Error was not reached", w.header)
	}
}

func TestPageGreetsByNameOrAsStranger(t *testing.T) {
	for name, want := range map[string]string{"": "Hello, stranger!", "Ana": "Hello, Ana!"} {
		got, err := xango.RenderString(Page(PageProps{Name: name}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "<h1>"+want+"</h1>") || !strings.Contains(got, "<title>gojiberry</title>") {
			t.Errorf("name %q: %s", name, got)
		}
	}
}
