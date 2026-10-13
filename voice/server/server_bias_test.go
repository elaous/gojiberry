package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/voice/listen"
)

func TestTranscribePassesPromptAndLanguageOnlyWhenAsked(t *testing.T) {
	var plain, biased int
	var gotOpts listen.Options
	s := newTestServer(nil, func(binary, model string, wav []byte) (string, error) { plain++; return "plain", nil })
	s.transcribeBias = func(binary, model string, wav []byte, o listen.Options) (string, error) {
		biased++
		gotOpts = o
		return "biased", nil
	}
	post := func(fields map[string]string) string {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("audio", "a.wav")
		_, _ = fw.Write([]byte("wav"))
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/transcribe", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if got := post(nil); !strings.Contains(got, "plain") || plain != 1 || biased != 0 {
		t.Fatalf("no fields use the plain path: %s %d %d", got, plain, biased)
	}
	if got := post(map[string]string{"prompt": "Gojiberry\x00 Samir", "language": "de"}); !strings.Contains(got, "biased") || biased != 1 {
		t.Fatalf("fields use the biased path: %s", got)
	}
	if gotOpts.Prompt != "Gojiberry Samir" || gotOpts.Language != "de" {
		t.Fatalf("options %+v", gotOpts)
	}
	post(map[string]string{"language": "de;rm"})
	if plain != 2 {
		t.Fatalf("a bad language alone is dropped, so the plain path: %d", plain)
	}
}
