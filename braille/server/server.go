package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Guard guard.Guard
	Addr  string // listen address, e.g. ":8085"

	LouTranslateBinary string
	Table              string
}

const defaultTable = "en-us-g2.ctb"

func ConfigFromEnv() Config {
	return Config{
		Guard:              guard.FromEnv("GOJIBERRY_BRAILLESERVER", "/health"),
		Addr:               envOr("GOJIBERRY_BRAILLESERVER_ADDR", ":8085"),
		LouTranslateBinary: os.Getenv("GOJIBERRY_BRAILLESERVER_LOU_TRANSLATE_BINARY"),
		Table:              envOr("GOJIBERRY_BRAILLESERVER_TABLE", defaultTable),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// never needs or exposes any API key
type Server struct {
	cfg Config

	translate func(binary, table, text string) (string, error)
}

// NewServer creates a braille server for the given config.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, translate: execLouTranslate}
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
	Text  string `json:"text"`
	Table string `json:"table"`
}

type translateResponse struct {
	Braille string `json:"braille"`
}

const maxTranslateBody = 2 * 1024 * 1024 // ~2MB of text, far past any realistic single document

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.LouTranslateBinary == "" {
		http.Error(w, "braille translation is not configured on this server (no lou_translate binary)", http.StatusServiceUnavailable)
		return
	}

	var req translateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxTranslateBody)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	table := s.cfg.Table
	if req.Table != "" {
		// only plain table names pass: a leading "-" would be an option
		if !tableNameRe.MatchString(req.Table) {
			http.Error(w, "invalid table name", http.StatusBadRequest)
			return
		}
		table = req.Table
	}
	select {
	case translateSlots <- struct{}{}:
		defer func() { <-translateSlots }()
	default:
		http.Error(w, "too many translations in progress", http.StatusTooManyRequests)
		return
	}

	done := observe.Default().Work("braille")
	braille, err := s.translate(s.cfg.LouTranslateBinary, table, req.Text)
	if err != nil {
		done("error")
		log.Printf("brailleserver: %v", err)
		http.Error(w, "translation failed", http.StatusBadGateway)
		return
	}
	done("ok")
	writeJSON(w, http.StatusOK, translateResponse{Braille: braille})
}

var tableNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._,-]{0,127}$`)

const translateTimeout = 15 * time.Second

var translateSlots = make(chan struct{}, 4)

func execLouTranslate(binary, table, text string) (string, error) {
	args := []string{}
	if table != "" {
		args = append(args, table)
	}
	ctx, cancel := context.WithTimeout(context.Background(), translateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("lou_translate: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
