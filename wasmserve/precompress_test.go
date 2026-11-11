package wasmserve

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("WASMSERVE_FAKE_BROTLI"); mode != "" {
		args := os.Args[1:]
		if mode == "fail" {
			fmt.Fprintln(os.Stderr, "brotli: out of memory")
			os.Exit(3)
		}
		var out, in string
		for i := 0; i < len(args); i++ {
			if args[i] == "-o" && i+1 < len(args) {
				out = args[i+1]
				i++
			} else if !strings.HasPrefix(args[i], "-") && args[i] != "11" {
				in = args[i]
			}
		}
		b, err := os.ReadFile(in)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(out, append([]byte("BR!"), b[:len(b)/10]...), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func gunzipFile(t *testing.T, p string) []byte {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func byName(cs []Compressed, name, enc string) *Compressed {
	for i := range cs {
		if cs[i].Name == name && cs[i].Encoding == enc {
			return &cs[i]
		}
	}
	return nil
}

func TestPrecompressWritesSiblingsThatHandlerSends(t *testing.T) {
	dir := t.TempDir()
	wasm := []byte(strings.Repeat("wasm bytes ", 2000))
	writeFile(t, dir, "main.wasm", wasm)
	writeFile(t, dir, "assets/app.css", []byte(strings.Repeat("body{margin:0}", 300)))
	writeFile(t, dir, "tiny.js", []byte("var a=1"))
	writeFile(t, dir, "logo.png", bytes.Repeat([]byte("png"), 1000))
	got, err := Precompress(dir, PrecompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c := byName(got, "main.wasm", "gzip"); c == nil || c.Action != "wrote" || c.Original != int64(len(wasm)) || c.Size >= c.Original {
		t.Fatalf("main.wasm: %+v in %+v", c, got)
	}
	if !bytes.Equal(gunzipFile(t, filepath.Join(dir, "main.wasm.gz")), wasm) {
		t.Error("the .gz does not decompress to the wasm")
	}
	if !exists(filepath.Join(dir, "assets", "app.css.gz")) {
		t.Error("a file in a subdirectory is compressed too")
	}
	for _, n := range []string{"tiny.js.gz", "logo.png.gz", "main.wasm.br"} {
		if exists(filepath.Join(dir, n)) {
			t.Errorf("%s should not exist: a small file, an image and a brotli tool that was not named are left out", n)
		}
	}

	h := Handler(os.DirFS(dir), Options{})
	rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	onDisk, _ := os.ReadFile(filepath.Join(dir, "main.wasm.gz"))
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Type") != "application/wasm" || !bytes.Equal(rec.Body.Bytes(), onDisk) {
		t.Fatalf("served %v, %d bytes (the file is %d)", rec.Header(), rec.Body.Len(), len(onDisk))
	}
}

func TestPrecompressKeepsRefreshesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "app.js", []byte(strings.Repeat("let x = 1;\n", 400)))
	if _, err := Precompress(dir, PrecompressOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Precompress(dir, PrecompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c := byName(got, "app.js", "gzip"); c == nil || c.Action != "kept" {
		t.Fatalf("an up-to-date sibling should be kept: %+v", got)
	}

	fresh := []byte(strings.Repeat("let y = 2;\n", 500))
	if err := os.WriteFile(src, fresh, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	got, err = Precompress(dir, PrecompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c := byName(got, "app.js", "gzip"); c == nil || c.Action != "wrote" || !bytes.Equal(gunzipFile(t, src+".gz"), fresh) {
		t.Fatalf("a rebuilt file needs a new sibling: %+v", got)
	}

	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Precompress(dir, PrecompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c := byName(got, "app.js", "gzip"); c == nil || c.Action != "removed" || exists(src+".gz") {
		t.Fatalf("a stale sibling must go: %+v", got)
	}
	writeFile(t, dir, "keep.js.gz", bytes.Repeat([]byte("z"), 5000))
	got, _ = Precompress(dir, PrecompressOptions{})
	if exists(filepath.Join(dir, "keep.js.gz.gz")) || byName(got, "keep.js.gz", "gzip") != nil {
		t.Error("a .gz was compressed again")
	}
}

func TestPrecompressLeavesWhatDoesNotShrink(t *testing.T) {
	dir := t.TempDir()
	noise := make([]byte, 4096)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "noise.js", noise)
	got, err := Precompress(dir, PrecompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "noise.js.gz")) {
		t.Fatalf("random bytes do not shrink, so no .gz: %+v", got)
	}
	rec := get(Handler(os.DirFS(dir), Options{}), "GET", "/noise.js", map[string]string{"Accept-Encoding": "gzip"})
	body := rec.Body.Bytes()
	if rec.Header().Get("Content-Encoding") == "gzip" { // compressed on the fly: still the same bytes
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(zr)
	}
	if !bytes.Equal(body, noise) {
		t.Error("the file is not served as it is")
	}
}

func TestPrecompressBrotliThroughTheTool(t *testing.T) {
	t.Setenv("WASMSERVE_FAKE_BROTLI", "ok")
	dir := t.TempDir()
	wasm := []byte(strings.Repeat("wasm bytes ", 2000))
	writeFile(t, dir, "main.wasm", wasm)
	got, err := Precompress(dir, PrecompressOptions{Brotli: os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	br, err := os.ReadFile(filepath.Join(dir, "main.wasm.br"))
	if err != nil || !bytes.HasPrefix(br, []byte("BR!")) {
		t.Fatalf("the .br the tool wrote: %q %v", br, err)
	}
	if c := byName(got, "main.wasm", "br"); c == nil || c.Action != "wrote" || c.Size != int64(len(br)) {
		t.Fatalf("report: %+v", got)
	}
	if exists(filepath.Join(dir, "main.wasm.br.tmp")) {
		t.Error("the temporary file was left behind")
	}
	h := Handler(os.DirFS(dir), Options{})
	if rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip, br"}); rec.Header().Get("Content-Encoding") != "br" || !bytes.Equal(rec.Body.Bytes(), br) {
		t.Errorf("br expected: %v", rec.Header())
	}
	if rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"}); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("gzip expected: %v", rec.Header())
	}
}

func TestPrecompressReportsAToolThatFails(t *testing.T) {
	t.Setenv("WASMSERVE_FAKE_BROTLI", "fail")
	dir := t.TempDir()
	writeFile(t, dir, "main.wasm", []byte(strings.Repeat("wasm bytes ", 2000)))
	_, err := Precompress(dir, PrecompressOptions{Brotli: os.Args[0]})
	if err == nil || !strings.Contains(err.Error(), "main.wasm") || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("the failure should name the file and the tool's message: %v", err)
	}
	if exists(filepath.Join(dir, "main.wasm.br")) {
		t.Error("a failed run left a .br")
	}
	if _, err := Precompress(dir, PrecompressOptions{Brotli: filepath.Join(dir, "no-such-tool")}); err == nil {
		t.Error("a missing tool is an error")
	}
}

func TestGzipLevelOfTheHandler(t *testing.T) {
	var text []byte
	for i := 0; i < 3000; i++ {
		text = append(text, fmt.Sprintf("line %d of some text with a pattern %d\n", i, i*7%13)...)
	}
	fsys := fstest.MapFS{"a.txt": {Data: text, ModTime: time.Unix(1000, 0)}}
	size := func(level int) int {
		h := Handler(fsys, Options{GzipLevel: level})
		rec := get(h, "GET", "/a.txt", map[string]string{"Accept-Encoding": "gzip"})
		zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := io.ReadAll(zr)
		if !bytes.Equal(plain, text) {
			t.Fatalf("level %d does not round trip", level)
		}
		return rec.Body.Len()
	}
	best, fast := size(0), size(gzip.BestSpeed) // 0 is the default, the strongest
	if best >= fast {
		t.Errorf("the default (%d bytes) should be smaller than BestSpeed (%d)", best, fast)
	}
}
