// Package wasmserve is the static file handler for a built wasm app. It does the four things a slow
// first load depends on (roadmap 82): it serves .wasm as application/wasm (browsers refuse
// WebAssembly.instantiateStreaming otherwise, falling back to a full download before compiling), it
// sends a precompressed .br or .gz file beside the original when the browser accepts it (a 26 MB
// wasm travels as about 7 MB), it compresses the other text-like files itself once and keeps the
// result, and it sets validators and cache headers so a repeat visit costs a 304.
//
//	http.Handle("/", wasmserve.Handler(os.DirFS("dist"), wasmserve.Options{Fallback: "index.html"}))
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
	"strings"
	"sync"
	"time"
)

// Options tune Handler. The zero value serves a directory as is.
type Options struct {
	// Fallback is the file served for a path that is not a file, for a single-page app whose
	// addresses like /d/report are handled in the page (usually "index.html"). A path with a file
	// extension that does not exist is still a 404, so a missing script is not answered with HTML.
	Fallback string
	// Immutable says which files may be cached forever (names with a content hash in them). The
	// default marks nothing: every file is revalidated with its ETag.
	Immutable func(name string) bool
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
	// The platform's table may lack it, and an unknown type here would break streaming compilation.
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
	}
	if h.opt.Immutable != nil && h.opt.Immutable(name) {
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hd.Set("Cache-Control", "no-cache") // store it, but ask with the ETag each time
	}
	http.ServeContent(w, r, "", v.modTime, bytes.NewReader(v.body))
}

// pick returns the best form of the file for the browser: a precompressed sibling, else our own
// gzip of a compressible file, else the file as it is.
func (h *handler) pick(name string, mod time.Time, accept string) (*variant, error) {
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	for _, c := range []struct{ enc, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !accepts(accept, c.enc) {
			continue
		}
		if st, err := fs.Stat(h.fsys, name+c.ext); err == nil && !st.IsDir() {
			return h.load(name+c.ext, c.enc, ctype, st.ModTime(), nil)
		}
	}
	if accepts(accept, "gzip") && compressible(ctype) {
		return h.load(name, "gzip", ctype, mod, gzipBytes)
	}
	return h.load(name, "", ctype, mod, nil)
}

// load reads a file (once per name, encoding and modification time) and, with compress, encodes it.
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
	for k := range h.cache { // a changed file replaces its older forms rather than piling up
		if strings.HasPrefix(k, name+"\x00"+enc+"\x00") {
			delete(h.cache, k)
		}
	}
	h.cache[key] = v
	return v, nil
}

func gzipBytes(b []byte) ([]byte, error) {
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
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

// compressible says which types gain from gzip (text-like ones and wasm; images and fonts are
// compressed already).
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

// accepts reports whether an Accept-Encoding header lists enc with a quality above zero.
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
