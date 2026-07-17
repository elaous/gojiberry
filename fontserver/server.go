// Package fontserver is a small standalone "font CDN": it serves a
// local directory of font files (see Repo) alongside a proxied,
// cached view of the Google Fonts catalog, behind one HTTP API a
// browser-side font picker can query the same way whether a family
// came from disk or from Google. It follows the same
// environment-configured, CORS-open, single-binary shape as
// gojiberry/syncserver -- see cmd/fontserver for the entry point.
package fontserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Config is loaded entirely from the environment, the same posture
// syncserver.Config takes: cloning this repo for a different local
// font collection, or turning Google Fonts proxying on, is always an
// environment variable, never a code change.
type Config struct {
	Addr           string        // listen address, e.g. ":8082"
	FontsDir       string        // local font directory Repo scans
	GoogleAPIKey   string        // Google Fonts Developer API key; empty disables the online catalog
	GoogleCacheTTL time.Duration // how long a fetched Google catalog is served before re-fetching
}

// ConfigFromEnv reads GOJIBERRY_FONTSERVER_ADDR (default ":8082"),
// GOJIBERRY_FONTSERVER_DIR (default "./fonts"),
// GOJIBERRY_FONTSERVER_GOOGLE_KEY (default "", meaning the online
// catalog is disabled -- see handleGoogleFonts), and
// GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL (a duration string like
// "24h", default 24h).
func ConfigFromEnv() Config {
	ttl := 24 * time.Hour
	if v := os.Getenv("GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			ttl = d
		}
	}
	return Config{
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

// googleFont is the subset of Google's webfonts API response
// (https://www.googleapis.com/webfonts/v1/webfonts) the picker needs:
// enough to render a preview and pick a variant, not the full
// response shape.
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

// Server hosts the local Repo and a cached view of the Google Fonts
// catalog behind one HTTP API.
type Server struct {
	cfg  Config
	repo *Repo

	// fetchGoogleFonts is swappable so tests can stub the network
	// call; production always uses fetchGoogleFontsAPI (set by
	// NewServer).
	fetchGoogleFonts func(apiKey string) ([]googleFont, error)

	mu            sync.Mutex
	googleCache   []googleFont
	googleFetched time.Time
	googleErr     error
}

// NewServer creates a font server for the given config, scanning the
// local font directory once up front (a scan failure is logged, not
// fatal -- an empty or unreadable directory still leaves the online
// catalog usable, and POST /local/rescan can retry later without a
// restart).
func NewServer(cfg Config) *Server {
	repo := NewRepo(cfg.FontsDir)
	if err := repo.Scan(); err != nil {
		fmt.Fprintf(os.Stderr, "fontserver: initial scan of %s: %v\n", cfg.FontsDir, err)
	}
	return &Server{cfg: cfg, repo: repo, fetchGoogleFonts: fetchGoogleFontsAPI}
}

// Handler returns the HTTP routes:
//
//	GET  /health                    -- confirms the server is up
//	GET  /local/fonts                -- the local Repo's cached family list
//	POST /local/rescan               -- re-scans the local font directory
//	GET  /local/files/{path}         -- the raw bytes of one scanned font file
//	GET  /local/symbols?family={name} -- a locally scanned family's own character repertoire (cmap.go's sfntCmapRanges), as JSON rune ranges
//	GET  /google/fonts               -- the cached Google Fonts catalog (empty, no error, if no API key is configured)
//
// Every response carries permissive CORS headers, the same reasoning
// as syncserver.Handler: this server's whole point is being reachable
// from a page served from a different origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/local/fonts", s.handleLocalFonts)
	mux.HandleFunc("/local/rescan", s.handleLocalRescan)
	mux.HandleFunc("/local/files/", s.handleLocalFile)
	mux.HandleFunc("/local/symbols", s.handleLocalSymbols)
	mux.HandleFunc("/google/fonts", s.handleGoogleFonts)
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
	if err := s.repo.Scan(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": s.repo.Families()})
}

func (s *Server) handleLocalFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// r.URL.Path is already percent-decoded by net/http's request
	// parser -- unescaping it again here would double-decode any
	// literal '%' in a font's relative path.
	rel := strings.TrimPrefix(r.URL.Path, "/local/files/")
	data, ok := s.repo.Open(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", fontContentType(rel))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data) // a write error here means the client disconnected -- nothing actionable to do about it
}

// handleLocalSymbols resolves ?family= to its representative variant
// (Repo.VariantPath), reads that file, and parses its own character
// repertoire (sfntCmapRanges, cmap.go) -- the special-character
// picker's own "browse this font's actual symbols" feature
// (OnDesign's specialchar.go), distinct from /local/fonts' plain
// family/variant listing. A family the Repo hasn't scanned, or whose
// representative file fails to parse as a well-formed sfnt font (a
// truncated/corrupt file on disk, say), is reported as 404/422
// respectively rather than an empty symbol list, so the caller can
// tell "no such font" and "no such symbols" apart from a real parse
// failure.
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

// handleGoogleFonts serves the cached Google Fonts catalog, fetching
// a fresh copy when the cache is older than GoogleCacheTTL. With no
// API key configured this returns an empty list and no error status --
// the same graceful-degradation posture index.html's Lora <link>
// already takes when a font fails to load -- so a picker built
// against this endpoint works whether or not the deployment has
// opted into the online catalog.
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
			// No cache to fall back on and the fetch failed: still 200,
			// still an empty list -- same reasoning as the no-key case,
			// a transient Google outage shouldn't surface as a picker
			// error the user has to do anything about.
			writeJSON(w, http.StatusOK, map[string]any{"families": []googleFont{}, "enabled": true, "error": fetchErr.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"families": cached, "enabled": true})
}

// fetchGoogleFontsAPI calls the real Google Fonts Developer API. The
// key travels only in this server-to-Google request, never to the
// browser -- handleGoogleFonts proxies the result, not the key.
func fetchGoogleFontsAPI(apiKey string) ([]googleFont, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	u := "https://www.googleapis.com/webfonts/v1/webfonts?key=" + url.QueryEscape(apiKey) + "&sort=popularity"
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
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
