package wasmserve

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// PrecompressOptions tune Precompress.
type PrecompressOptions struct {
	// MinSize is the size under which a file is left alone (compressing it saves little and costs a
	// request's worth of headers). 0 means 1024 bytes.
	MinSize int64
	// Brotli is the path of the brotli command line tool (or just "brotli", found on the PATH). Empty
	// writes no .br files: the Go standard library has no brotli encoder, and the tool is the usual
	// way to get one. It is called as `brotli -q 11 -f -o <out> <in>`.
	Brotli string
}

// Compressed is one precompressed file Precompress wrote, kept, or removed.
type Compressed struct {
	// Name is the file the compressed form belongs to, relative to the directory, with slashes.
	Name string
	// Encoding is "gzip" (written as Name+".gz") or "br" (Name+".br").
	Encoding string
	// Original and Size are the bytes of the file and of its compressed form.
	Original, Size int64
	// Action is "wrote" (made or refreshed), "kept" (an up-to-date sibling was already there) or
	// "removed" (a sibling that did not save anything, or that outlived a file that is now too
	// small or no longer compressible).
	Action string
}

// Precompress writes a .gz (and, with PrecompressOptions.Brotli, a .br) next to every compressible file
// of dir (wasm, scripts, styles, pages, JSON, SVG: the types Handler compresses on the fly) that is at
// least MinSize bytes and shrinks by it. The compressed files are what Handler, nginx's gzip_static and
// brotli_static, or Caddy's precompressed option send, so the server does no compression per request
// and a wasm of tens of megabytes is compressed once, at the strongest level. A sibling at least as
// new as its file is kept. Files that already end in .gz or .br are not touched.
func Precompress(dir string, opt PrecompressOptions) ([]Compressed, error) {
	if opt.MinSize == 0 {
		opt.MinSize = 1024
	}
	var out []Compressed
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		ext := path.Ext(p)
		if ext == ".gz" || ext == ".br" {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		ctype := mime.TypeByExtension(ext)
		eligible := compressible(ctype) && info.Size() >= opt.MinSize
		encs := []struct{ name, ext string }{{"gzip", ".gz"}}
		if opt.Brotli != "" {
			encs = append(encs, struct{ name, ext string }{"br", ".br"})
		}
		for _, e := range encs {
			sib := p + e.ext
			st, serr := os.Stat(sib)
			have := serr == nil
			if !eligible {
				if have {
					if err := os.Remove(sib); err != nil {
						return err
					}
					out = append(out, Compressed{rel, e.name, info.Size(), st.Size(), "removed"})
				}
				continue
			}
			if have && !st.ModTime().Before(info.ModTime()) {
				out = append(out, Compressed{rel, e.name, info.Size(), st.Size(), "kept"})
				continue
			}
			size, err := writeCompressed(p, sib, e.name, opt.Brotli)
			if err != nil {
				return fmt.Errorf("wasmserve: compressing %s: %w", rel, err)
			}
			if size >= info.Size() { // no gain: the file is better sent as it is
				if err := os.Remove(sib); err != nil {
					return err
				}
				out = append(out, Compressed{rel, e.name, info.Size(), size, "removed"})
				continue
			}
			out = append(out, Compressed{rel, e.name, info.Size(), size, "wrote"})
		}
		return nil
	})
	return out, err
}

// writeCompressed writes the compressed form of src to dst through a temporary file, so a stop in
// the middle never leaves half a file that a server would send as the whole, and returns its size.
func writeCompressed(src, dst, enc, brotli string) (int64, error) {
	tmp := dst + ".tmp"
	defer func() { _ = os.Remove(tmp) }()
	switch enc {
	case "gzip":
		b, err := os.ReadFile(src)
		if err != nil {
			return 0, err
		}
		z, err := gzipBytes(b)
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(tmp, z, 0o644); err != nil {
			return 0, err
		}
	default:
		var stderr bytes.Buffer
		cmd := exec.Command(brotli, "-q", "11", "-f", "-o", tmp, src)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return 0, fmt.Errorf("%s: %w: %s", brotli, err, strings.TrimSpace(stderr.String()))
		}
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return 0, err
	}
	// The sibling must not look older than the file it was made from, whatever the clock did.
	now := time.Now()
	if err := os.Chtimes(dst, now, now); err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// gzipLevel is the compression level the handler uses for what it compresses itself.
func (o Options) gzipLevel() int {
	if o.GzipLevel == 0 {
		return gzip.BestCompression
	}
	return o.GzipLevel
}
