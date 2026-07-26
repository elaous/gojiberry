// Package brailleserver is a small standalone braille-transcription
// proxy: real contracted (Grade 2) or uncontracted (Grade 1) braille
// output via a local liblouis (https://liblouis.io) install, the
// standard, actively-maintained open-source braille translation
// engine (used by NVDA's own braille support among others) and the
// only realistic foundation for this -- correctly applying Grade 2's
// own contraction rules is language-dependent, standardized, non-
// trivial logic no one should hand-roll here. liblouis is a C library
// with no native Go binding in this stack; it ships its own
// `lou_translate` CLI, so -- the exact shape gojiberry/voiceserver
// already establishes for exec'd, optionally-configured external
// tooling (PiperBinary/WhisperBinary) -- this package execs it as a
// subprocess per request rather than embedding a WASM/cgo binding.
// Same environment-configured, CORS-open, single-binary shape as
// gojiberry/syncserver, gojiberry/fontserver, gojiberry/translateserver,
// and gojiberry/voiceserver -- see cmd/brailleserver for the entry
// point.
package brailleserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// Config is loaded entirely from the environment, the same posture
// voiceserver.Config/translateserver.Config take.
type Config struct {
	Addr string // listen address, e.g. ":8085"

	// LouTranslateBinary/Table gate /translate the same way
	// voiceserver.Config.PiperBinary gates /speak: with
	// LouTranslateBinary empty, /translate always reports 503. Table is
	// the default liblouis translation table (e.g. "en-us-g2.ctb" for
	// contracted Grade 2 English) used when a request doesn't name its
	// own -- see liblouis's own tables/ directory for every table name
	// a real install ships.
	LouTranslateBinary string
	Table              string
}

// defaultTable is Grade 2 (contracted) US English -- the table most
// real embosser/braille-display workflows expect by default, matching
// this task's own "Grade 1/Grade 2" framing with Grade 2 as the
// primary target, not just an uncontracted passthrough.
const defaultTable = "en-us-g2.ctb"

// ConfigFromEnv reads GOJIBERRY_BRAILLESERVER_ADDR (default ":8085"),
// GOJIBERRY_BRAILLESERVER_LOU_TRANSLATE_BINARY (empty by default -- see
// NewServer's own doc comment for what that means for /translate), and
// GOJIBERRY_BRAILLESERVER_TABLE (default defaultTable).
func ConfigFromEnv() Config {
	return Config{
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

// Server proxies braille-translation requests to a local liblouis
// install, never needing (or exposing) any API key.
type Server struct {
	cfg Config

	// translate is swappable so tests can inject a fake instead of
	// shelling out to a real lou_translate binary -- the same
	// "swappable network/subprocess call" reasoning voiceserver's own
	// speak/transcribe fields document.
	translate func(binary, table, text string) (string, error)
}

// NewServer creates a braille server for the given config. With
// LouTranslateBinary empty, /translate stays wired but always reports
// 503 -- the same "still runs, just reports the feature is off"
// posture voiceserver.NewServer takes with no Piper/whisper binary
// configured, rather than refusing to start at all.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, translate: execLouTranslate}
}

// Handler returns the HTTP routes:
//
//	GET  /health     -- confirms the server is up
//	POST /translate  -- {"text", "table"(optional)} -> {"braille"}
//
// Every response carries permissive CORS headers, the same reasoning
// as syncserver/fontserver/translateserver/voiceserver's own Handler:
// this server's whole point is being reachable from a page served
// from a different origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/translate", s.handleTranslate)
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type translateRequest struct {
	Text string `json:"text"`
	// Table overrides cfg.Table for this one request when non-empty --
	// a caller wanting Grade 1 (uncontracted) output instead of the
	// server's own Grade 2 default, say, passes "en-us-g1.ctb" here
	// rather than needing a second deployment.
	Table string `json:"table"`
}

type translateResponse struct {
	Braille string `json:"braille"`
}

// maxTranslateBody caps the request body at a generous but finite
// size, the same defensive posture translateserver's own
// maxTranslateBody/voiceserver's own maxSpeakBody take against a
// client-supplied Content-Length that can't be trusted.
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
	table := req.Table
	if table == "" {
		table = s.cfg.Table
	}

	braille, err := s.translate(s.cfg.LouTranslateBinary, table, req.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, translateResponse{Braille: braille})
}

// execLouTranslate is translate's real implementation: pipes text into
// lou_translate's own stdin and reads the ASCII-braille text it writes
// to stdout -- the well-established liblouis CLI invocation shape,
// `echo "text" | lou_translate table.ctb`. ASCII braille (liblouis's
// own default output encoding, not Unicode braille patterns) is
// exactly BRF's own on-the-wire representation -- the North American
// Braille ASCII Grade 2 embossers and braille-display software both
// consume -- so this output needs no further re-encoding downstream.
func execLouTranslate(binary, table, text string) (string, error) {
	args := []string{}
	if table != "" {
		args = append(args, table)
	}
	cmd := exec.Command(binary, args...)
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
