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
	MinSize int64
	Brotli  string
}

// Compressed is one precompressed file Precompress wrote, kept, or removed.
type Compressed struct {
	Name string
	// Encoding is "gzip" (written as Name+".gz") or "br" (Name+".br").
	Encoding       string
	Original, Size int64
	Action         string
}

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

// temp file then rename: a stop never leaves half a file
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
	// the sibling must not look older than its source
	now := time.Now()
	if err := os.Chtimes(dst, now, now); err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (o Options) gzipLevel() int {
	if o.GzipLevel == 0 {
		return gzip.BestCompression
	}
	return o.GzipLevel
}
