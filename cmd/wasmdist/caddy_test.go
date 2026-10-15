package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// caddyBlock is the Caddy recipe of docs/deploy.md, as a site on a local port (the block in the page uses a host
// name and a directory the reader sets). Keep the two the same.
const caddyBlock = `{
	admin off
	auto_https off
}
%s {
	root * %s
	encode zstd gzip       # for files that have no precompressed sibling
	@scripts path_regexp ^.+\.(js|css|wasm|json|png|svg|ico)$
	handle @scripts {
		file_server {
			precompressed br gzip
		}
	}
	handle {
		try_files {path} /index.html
		file_server
	}
	header Cache-Control "no-cache"
}
`

func startCaddy(t *testing.T, dir string) string {
	t.Helper()
	bin, err := exec.LookPath("caddy")
	if p := os.Getenv("GOJIBERRY_CADDY"); p != "" {
		bin, err = p, nil
	}
	if err != nil {
		if os.Getenv("GOJIBERRY_REQUIRE_CADDY") != "" {
			t.Fatal("caddy is required and was not found")
		}
		t.Skip("caddy is not installed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	tmp := t.TempDir()
	conf := filepath.Join(tmp, "Caddyfile")
	if err := os.WriteFile(conf, []byte(fmt.Sprintf(caddyBlock, "http://"+addr, dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "--config", conf, "--adapter", "caddyfile")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+tmp, "XDG_CONFIG_HOME="+tmp)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("caddy said:\n%s", out.String())
		}
	})
	for i := 0; i < 200; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			return "http://" + addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("caddy did not start: %s", out.String())
	return ""
}

// The Caddy recipe of docs/deploy.md run against a real Caddy: the precompressed file in the encoding the browser
// asks for (brotli, then gzip), the file itself otherwise, the wasm type, a 304 for a repeat visit, a 404 for a
// script that is not there, and the start page for an app path.
func TestShowcaseCaddyRecipe(t *testing.T) {
	dir, wasm := site(t)
	brotli := "off"
	if p, err := exec.LookPath("brotli"); err == nil {
		brotli = p
	}
	if err := run([]string{"-brotli", brotli, dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	base := startCaddy(t, dir)
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, hdr map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}

	if brotli != "off" {
		res, body := get("/main.wasm", map[string]string{"Accept-Encoding": "br, gzip"})
		sibling, _ := os.ReadFile(filepath.Join(dir, "main.wasm.br"))
		if res.Header.Get("Content-Encoding") != "br" || !bytes.Equal(body, sibling) || res.Header.Get("Content-Type") != "application/wasm" {
			t.Errorf("main.wasm for br: %q %q, %d bytes against %d", res.Header.Get("Content-Encoding"), res.Header.Get("Content-Type"), len(body), len(sibling))
		}
	}
	res, body := get("/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	sibling, _ := os.ReadFile(filepath.Join(dir, "main.wasm.gz"))
	if res.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(body, sibling) || res.Header.Get("Content-Type") != "application/wasm" {
		t.Errorf("main.wasm for gzip: %q %q, %d bytes against %d", res.Header.Get("Content-Encoding"), res.Header.Get("Content-Type"), len(body), len(sibling))
	}
	res, body = get("/main.wasm", map[string]string{"Accept-Encoding": "identity"})
	if res.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, wasm) {
		t.Errorf("main.wasm for identity: %q, %d bytes", res.Header.Get("Content-Encoding"), len(body))
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if etag := res.Header.Get("ETag"); etag == "" {
		t.Error("no ETag")
	} else if res, body = get("/main.wasm", map[string]string{"If-None-Match": etag, "Accept-Encoding": "identity"}); res.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("a repeat visit: %d with %d bytes, want 304 and none", res.StatusCode, len(body))
	}
	if res, _ = get("/nothing.js", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("a missing script is %d, want 404", res.StatusCode)
	}
	res, body = get("/d/report", map[string]string{"Accept-Encoding": "identity"})
	if res.StatusCode != 200 || !strings.HasPrefix(string(body), "<!doctype html>") {
		t.Errorf("/d/report: %d %.40q", res.StatusCode, body)
	}
}

// docs/deploy.md shows the block this test runs: every line of it is in the page.
func TestDeployDocShowsTheTestedCaddyBlock(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "deploy.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(fmt.Sprintf(caddyBlock, "example.com", "/srv/showcase"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "{" || line == "}" || line == "admin off" || line == "auto_https off" {
			continue
		}
		if !strings.Contains(string(page), line) {
			t.Errorf("docs/deploy.md does not show the line %q of the tested Caddy block", line)
		}
	}
}
