package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text, TargetLang, SourceLang string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/translate" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if req.TargetLang == "XX" {
			http.Error(w, "unknown language", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"translatedText": strings.ToUpper(req.Text) + "/" + req.TargetLang, "detectedSourceLanguage": "EN"})
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL + "/", Header: http.Header{"Authorization": {"Bearer k"}}}
	got, err := c.Translate(context.Background(), "hello", "DE", "")
	if err != nil || got.Text != "HELLO/DE" || got.Detected != "EN" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := c.Translate(context.Background(), "hello", "XX", ""); err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "unknown language") {
		t.Errorf("a refusal must be an error that says why: %v", err)
	}
	if r, err := c.Translate(context.Background(), "  ", "DE", ""); err != nil || r.Text != "" {
		t.Errorf("blank text: %+v %v", r, err)
	}
	if _, err := (Client{BaseURL: srv.URL}).Translate(context.Background(), "x", "DE", ""); err == nil {
		t.Error("no token, no translation")
	}
}
