package wasmserve

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options tune Handler.
type Options struct {
	Fallback      string
	Immutable     func(name string) bool
	GzipLevel     int
	ServiceWorker bool
}

type variant struct {
	body     []byte
	encoding string // "" identity, "br" or "gzip"
	etag     string
	modTime  time.Time
	ctype    string
}

type handler struct {
	fsys fs.FS
	opt  Options

	mu    sync.Mutex
	cache map[string]*variant // by file name + encoding + mod time
}

// Handler serves the files of fsys.
func Handler(fsys fs.FS, opt Options) http.Handler {
	return &handler{fsys: fsys, opt: opt, cache: map[string]*variant{}}
}

func init() {
	// the platform table may lack it: unknown type breaks streaming
	_ = mime.AddExtensionType(".wasm", "application/wasm")
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if h.opt.ServiceWorker && name == "sw.js" {
		ServeServiceWorker(w, r, h.fsys, nil)
		return
	}
	info, err := fs.Stat(h.fsys, name)
	if err == nil && info.IsDir() {
		name = path.Join(name, "index.html")
		info, err = fs.Stat(h.fsys, name)
	}
	if err != nil || info.IsDir() {
		if h.opt.Fallback == "" || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = h.opt.Fallback
		if info, err = fs.Stat(h.fsys, name); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	v, err := h.pick(name, info.ModTime(), r.Header.Get("Accept-Encoding"))
	if err != nil {
		http.Error(w, "could not read the file", http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", v.ctype)
	hd.Set("Etag", v.etag)
	hd.Set("Vary", "Accept-Encoding")
	if v.encoding != "" {
		hd.Set("Content-Encoding", v.encoding)
		if r.Header.Get("Range") == "" {
			hd.Set("Content-Length", strconv.Itoa(len(v.body)))
		}
	}
	if h.opt.Immutable != nil && h.opt.Immutable(name) {
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hd.Set("Cache-Control", "no-cache") // store it, but ask with the ETag each time
	}
	http.ServeContent(w, r, "", v.modTime, bytes.NewReader(v.body))
}

func (h *handler) pick(name string, mod time.Time, accept string) (*variant, error) {
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	for _, c := range []struct{ enc, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !accepts(accept, c.enc) {
			continue
		}
		if st, err := fs.Stat(h.fsys, name+c.ext); err == nil && !st.IsDir() && !st.ModTime().Before(mod) {
			return h.load(name+c.ext, c.enc, ctype, st.ModTime(), nil)
		}
	}
	if accepts(accept, "gzip") && compressible(ctype) {
		return h.load(name, "gzip", ctype, mod, func(b []byte) ([]byte, error) { return gzipLevelBytes(b, h.opt.gzipLevel()) })
	}
	return h.load(name, "", ctype, mod, nil)
}

func (h *handler) load(name, enc, ctype string, mod time.Time, compress func([]byte) ([]byte, error)) (*variant, error) {
	key := name + "\x00" + enc + "\x00" + mod.UTC().Format(time.RFC3339Nano)
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := h.cache[key]; ok {
		return v, nil
	}
	f, err := h.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if compress != nil {
		if b, err = compress(b); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(b)
	v := &variant{body: b, encoding: enc, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, modTime: mod, ctype: ctype}
	for k := range h.cache {
		if strings.HasPrefix(k, name+"\x00"+enc+"\x00") {
			delete(h.cache, k)
		}
	}
	h.cache[key] = v
	return v, nil
}

func gzipBytes(b []byte) ([]byte, error) { return gzipLevelBytes(b, gzip.BestCompression) }

func gzipLevelBytes(b []byte, level int) ([]byte, error) {
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func compressible(ctype string) bool {
	ctype, _, _ = strings.Cut(ctype, ";")
	switch {
	case strings.HasPrefix(ctype, "text/"):
		return true
	}
	switch ctype {
	case "application/wasm", "application/javascript", "application/json", "image/svg+xml", "application/xml":
		return true
	}
	return false
}

func accepts(header, enc string) bool {
	for _, part := range strings.Split(header, ",") {
		tok, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(tok), enc) {
			p := strings.ReplaceAll(strings.ToLower(params), " ", "")
			return p != "q=0" && p != "q=0.0" && p != "q=0.00" && p != "q=0.000"
		}
	}
	return false
}
