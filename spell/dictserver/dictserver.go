package dictserver

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Server serves the dictionaries of FS.
type Server struct {
	FS       fs.FS
	MaxBytes int64
	Names    map[string]string

	mu    sync.Mutex
	cache map[string]*file
}

type file struct {
	data []byte // as stored
	gz   bool
	etag string
}

// Language is one entry of index.json.
type Language struct {
	Lang  string `json:"lang"`
	Name  string `json:"name,omitempty"`
	Bytes int64  `json:"bytes"`
}

var langRE = regexp.MustCompile(`^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8}){0,2}$`)

func (s *Server) max() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return 64 << 20
}

// Languages lists the languages of FS that have both files.
func (s *Server) Languages() ([]Language, error) {
	entries, err := fs.ReadDir(s.FS, ".")
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if b, err := fs.ReadFile(s.FS, "names.json"); err == nil {
		_ = json.Unmarshal(b, &names)
	}
	for k, v := range s.Names {
		names[k] = v
	}
	var out []Language
	for _, e := range entries {
		if !e.IsDir() || !langRE.MatchString(e.Name()) {
			continue
		}
		var total int64
		ok := true
		for _, ext := range []string{"aff", "dic"} {
			fi, _, err := s.stat(e.Name(), ext)
			if err != nil {
				ok = false
				break
			}
			total += fi
		}
		if ok {
			out = append(out, Language{Lang: e.Name(), Name: names[e.Name()], Bytes: total})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lang < out[j].Lang })
	return out, nil
}

func (s *Server) stat(lang, ext string) (size int64, gz bool, err error) {
	for _, name := range []string{path.Join(lang, lang+"."+ext), path.Join(lang, lang+"."+ext+".gz")} {
		if fi, err := fs.Stat(s.FS, name); err == nil && !fi.IsDir() {
			return fi.Size(), strings.HasSuffix(name, ".gz"), nil
		}
	}
	return 0, false, fs.ErrNotExist
}

func (s *Server) load(lang, ext string) (*file, error) {
	key := lang + "." + ext
	s.mu.Lock()
	if f := s.cache[key]; f != nil {
		s.mu.Unlock()
		return f, nil
	}
	s.mu.Unlock()
	_, gz, err := s.stat(lang, ext)
	if err != nil {
		return nil, err
	}
	name := path.Join(lang, lang+"."+ext)
	if gz {
		name += ".gz"
	}
	f, err := s.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, s.max()+1))
	if err != nil {
		return nil, err
	}
	if gz {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(io.Discard, io.LimitReader(zr, s.max()+1))
		if err != nil || n > s.max() {
			return nil, fs.ErrInvalid
		}
	} else if int64(len(data)) > s.max() {
		return nil, fs.ErrInvalid
	}
	sum := sha256.Sum256(data)
	out := &file{data: data, gz: gz, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]*file{}
	}
	s.cache[key] = out
	s.mu.Unlock()
	return out, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "index.json" {
		langs, err := s.Languages()
		if err != nil {
			http.Error(w, "dictionaries unavailable", http.StatusInternalServerError)
			return
		}
		if langs == nil {
			langs = []Language{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(langs)
		return
	}
	base, ext, ok := strings.Cut(name, ".")
	if !ok || (ext != "aff" && ext != "dic") || !langRE.MatchString(base) {
		http.NotFound(w, r)
		return
	}
	f, err := s.load(base, ext)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", f.etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Add("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == f.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.data
	if f.gz {
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
		} else {
			zr, err := gzip.NewReader(bytes.NewReader(f.data))
			if err != nil {
				http.Error(w, "damaged file", http.StatusInternalServerError)
				return
			}
			body, err = io.ReadAll(io.LimitReader(zr, s.max()+1))
			if err != nil {
				http.Error(w, "damaged file", http.StatusInternalServerError)
				return
			}
		}
	}
	_, _ = w.Write(body)
}
