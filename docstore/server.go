package docstore

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

// Config is loaded entirely from the environment, mirroring
// syncserver/fontserver's convention: cloning this repo for a
// different operator or backend never requires a code change.
//
// Backend selects which Adapter NewServerFromConfig builds:
//
//	"memory"   (default) -- MemoryAdapter, no persistence, dev/test only
//	"localfs"  -- LocalFSAdapter, rooted at LocalFSDir
//	"postgres" -- PostgresAdapter, connecting to PostgresDSN
//	"mongo"    -- MongoAdapter, connecting to MongoURI/MongoDB
//	"s3"       -- S3Adapter, backed by S3Bucket (region/credentials
//	              come from the AWS SDK's own standard env vars)
type Config struct {
	Addr    string
	Backend string

	LocalFSDir string

	PostgresDSN string

	MongoURI string
	MongoDB  string

	S3Bucket string
	S3Prefix string
}

// ConfigFromEnv reads GOJIBERRY_DOCSTORE_*.
func ConfigFromEnv() Config {
	return Config{
		Addr:        envOr("GOJIBERRY_DOCSTORE_ADDR", ":8090"),
		Backend:     envOr("GOJIBERRY_DOCSTORE_BACKEND", "memory"),
		LocalFSDir:  envOr("GOJIBERRY_DOCSTORE_LOCALFS_DIR", "./docstore-data"),
		PostgresDSN: os.Getenv("GOJIBERRY_DOCSTORE_POSTGRES_DSN"),
		MongoURI:    os.Getenv("GOJIBERRY_DOCSTORE_MONGO_URI"),
		MongoDB:     envOr("GOJIBERRY_DOCSTORE_MONGO_DB", "docstore"),
		S3Bucket:    os.Getenv("GOJIBERRY_DOCSTORE_S3_BUCKET"),
		S3Prefix:    os.Getenv("GOJIBERRY_DOCSTORE_S3_PREFIX"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server exposes an Adapter over HTTP:
//
//	PUT    /docs/{id}  -- save (request body is the raw document bytes)
//	GET    /docs/{id}  -- load (response body is the raw document bytes)
//	DELETE /docs/{id}  -- delete
//	GET    /docs       -- list metadata, as a JSON array of Meta
//	GET    /health     -- liveness + configured backend name
type Server struct {
	adapter Adapter
	backend string
}

// NewServer wraps an already-constructed Adapter. Use this directly
// when embedding docstore in another program that builds its own
// Adapter; use NewServerFromConfig to select a backend by Config.
func NewServer(adapter Adapter, backendName string) *Server {
	return &Server{adapter: adapter, backend: backendName}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/docs", s.handleList)
	mux.HandleFunc("/docs/", s.handleDoc)
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "backend": s.backend})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metas, err := s.adapter.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metas)
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/docs/")
	if id == "" {
		http.Error(w, "missing document id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.adapter.Save(r.Context(), id, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		doc, err := s.adapter.Load(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(doc.Data)
	case http.MethodDelete:
		err := s.adapter.Delete(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
