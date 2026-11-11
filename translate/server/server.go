package server

import (
	"context"
	"encoding/json"
	"github.com/elaous/gojiberry/observe"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/elaous/gojiberry/guard"
)

type Config struct {
	Guard       guard.Guard
	Addr        string // listen address, e.g. ":8083"
	DeepLAPIKey string // DeepL API authentication key; empty disables translation
}

func ConfigFromEnv() Config {
	return Config{
		Guard:       guard.FromEnv("GOJIBERRY_TRANSLATESERVER", "/health"),
		Addr:        envOr("GOJIBERRY_TRANSLATESERVER_ADDR", ":8083"),
		DeepLAPIKey: os.Getenv("GOJIBERRY_TRANSLATESERVER_DEEPL_KEY"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type translator interface {
	Translate(ctx context.Context, text []string, targetLang, sourceLang string) ([]Translation, error)
}

// never exposes the API key to the browser
type Server struct {
	cfg        Config
	translator translator // nil if cfg.DeepLAPIKey is empty
}

// NewServer creates a translate server for the given config.
func NewServer(cfg Config) (*Server, error) {
	s := &Server{cfg: cfg}
	if cfg.DeepLAPIKey != "" {
		t, err := newDeeplClient(cfg.DeepLAPIKey)
		if err != nil {
			return nil, err
		}
		s.translator = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/translate", s.handleTranslate)
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type translateRequest struct {
	Text       string `json:"text"`
	TargetLang string `json:"targetLang"`
	SourceLang string `json:"sourceLang"` // optional; DeepL auto-detects when empty
}

type translateResponse struct {
	TranslatedText         string `json:"translatedText"`
	DetectedSourceLanguage string `json:"detectedSourceLanguage"`
}

const maxTranslateBody = 200 * 1024

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.translator == nil {
		http.Error(w, "translation is not configured on this server (no DeepL API key)", http.StatusServiceUnavailable)
		return
	}

	var req translateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxTranslateBody)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Text == "" || req.TargetLang == "" {
		http.Error(w, "text and targetLang are required", http.StatusBadRequest)
		return
	}

	done := observe.Default().Work("translate")
	results, err := s.translator.Translate(r.Context(), []string{req.Text}, req.TargetLang, req.SourceLang)
	if err != nil {
		done("error")
		log.Printf("translateserver: %v", err)
		http.Error(w, "the translation service failed", http.StatusBadGateway)
		return
	}
	if len(results) == 0 {
		done("error")
		http.Error(w, "translation returned no results", http.StatusBadGateway)
		return
	}
	done("ok")

	writeJSON(w, http.StatusOK, translateResponse{
		TranslatedText:         results[0].Text,
		DetectedSourceLanguage: results[0].DetectedSourceLanguage,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
