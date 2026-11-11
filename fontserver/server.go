package fontserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Guard          guard.Guard
	Addr           string        // listen address, e.g. ":8082"
	FontsDir       string        // local font directory Repo scans
	GoogleAPIKey   string        // Google Fonts Developer API key; empty disables the online catalog
	GoogleCacheTTL time.Duration // how long a fetched Google catalog is served before re-fetching
}

func ConfigFromEnv() Config {
	ttl := 24 * time.Hour
	if v := os.Getenv("GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			ttl = d
		}
	}
	return Config{
		Guard:          guard.FromEnv("GOJIBERRY_FONTSERVER", "/health"),
		Addr:           envOr("GOJIBERRY_FONTSERVER_ADDR", ":8082"),
		FontsDir:       envOr("GOJIBERRY_FONTSERVER_DIR", "./fonts"),
		GoogleAPIKey:   os.Getenv("GOJIBERRY_FONTSERVER_GOOGLE_KEY"),
		GoogleCacheTTL: ttl,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type googleFont struct {
	Family   string            `json:"family"`
	Category string            `json:"category"`
	Variants []string          `json:"variants"`
	Subsets  []string          `json:"subsets"`
	Files    map[string]string `json:"files"`
}

type googleFontsListResponse struct {
	Items []googleFont `json:"items"`
}

type Server struct {
	cfg  Config
	repo *Repo

	fetchGoogleFonts     func(apiKey string) ([]googleFont, error)
	fetchGoogleFontFiles func(family string) ([]googleFontFile, error)

	downloads chan struct{} // limits concurrent /local/download requests

	mu            sync.Mutex
	googleCache   []googleFont
	googleFetched time.Time
	googleErr     error
}

func NewServer(cfg Config) *Server {
	repo := NewRepo(cfg.FontsDir)
	if err := repo.Scan(); err != nil {
		fmt.Fprintf(os.Stderr, "fontserver: initial scan of %s: %v\n", cfg.FontsDir, err)
	}
	return &Server{cfg: cfg, repo: repo, fetchGoogleFonts: fetchGoogleFontsAPI, fetchGoogleFontFiles: fetchGoogleFontFilesAPI, downloads: make(chan struct{}, 2)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/local/fonts", s.handleLocalFonts)
	mux.HandleFunc("/local/rescan", s.handleLocalRescan)
	mux.HandleFunc("/local/files/", worked("font_file", s.handleLocalFile))
	mux.HandleFunc("/local/symbols", worked("font_symbols", s.handleLocalSymbols))
	mux.HandleFunc("/google/fonts", worked("google_fonts", s.handleGoogleFonts))
	mux.HandleFunc("/local/download", worked("font_download", s.handleLocalDownload))
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func worked(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		done := observe.Default().Work(name)
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(sw, r)
		switch {
		case sw.status >= 500:
			done("error")
		case sw.status >= 400:
			done("rejected")
		default:
			done("ok")
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLocalFonts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": s.repo.Families()})
}

func (s *Server) handleLocalRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	done := observe.Default().Work("font_rescan")
	if err := s.repo.Scan(); err != nil {
		done("error")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	done("ok")
	writeJSON(w, http.StatusOK, map[string]any{"families": s.repo.Families()})
}

func (s *Server) handleLocalFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Path is already decoded: unescaping again would double-decode '%'
	rel := strings.TrimPrefix(r.URL.Path, "/local/files/")
	data, ok := s.repo.Open(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", fontContentType(rel))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) handleLocalSymbols(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	family := r.URL.Query().Get("family")
	if family == "" {
		http.Error(w, "missing family parameter", http.StatusBadRequest)
		return
	}
	path, ok := s.repo.VariantPath(family)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, ok := s.repo.Open(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ranges, err := sfntCmapRanges(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeJSON(w, http.StatusOK, map[string]any{"ranges": ranges})
}

func fontContentType(path string) string {
	if strings.HasSuffix(strings.ToLower(path), ".otf") {
		return "font/otf"
	}
	return "font/ttf"
}

func (s *Server) handleGoogleFonts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.GoogleAPIKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"families": []googleFont{}, "enabled": false})
		return
	}

	s.mu.Lock()
	stale := time.Since(s.googleFetched) > s.cfg.GoogleCacheTTL
	cached := s.googleCache
	s.mu.Unlock()

	if stale {
		fresh, err := s.fetchGoogleFonts(s.cfg.GoogleAPIKey)
		s.mu.Lock()
		if err == nil {
			s.googleCache = fresh
			s.googleFetched = time.Now()
			s.googleErr = nil
		} else {
			s.googleErr = err
		}
		cached = s.googleCache
		fetchErr := s.googleErr
		s.mu.Unlock()
		if err != nil && cached == nil {
			log.Printf("fontserver: %v", fetchErr)
			writeJSON(w, http.StatusOK, map[string]any{"families": []googleFont{}, "enabled": true, "error": "google fonts is unavailable"})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"families": cached, "enabled": true})
}

// the key never reaches the browser
func fetchGoogleFontsAPI(apiKey string) ([]googleFont, error) {
	return fetchGoogleFontsFrom("https://www.googleapis.com/webfonts/v1/webfonts?key="+url.QueryEscape(apiKey)+"&sort=popularity", nil)
}

func fetchGoogleFontsFrom(u string, client *http.Client) ([]googleFont, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Get(u)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("fontserver: google fonts request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fontserver: google fonts api returned %s: %s", resp.Status, body)
	}
	var out googleFontsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
