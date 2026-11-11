package main

import (
	"bytes"
	"compress/gzip"
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

func startNginx(t *testing.T, dir string) string {
	t.Helper()
	bin, err := exec.LookPath("nginx")
	if err != nil {
		if os.Getenv("GOJIBERRY_REQUIRE_NGINX") != "" {
			t.Fatal("nginx is required and was not found")
		}
		t.Skip("nginx is not installed")
	}
	block, err := os.ReadFile(filepath.Join("testdata", "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	srv := strings.NewReplacer("listen 80;", "listen "+addr+";", "root /usr/share/nginx/html;", "root "+dir+";").Replace(string(block))
	if !strings.Contains(srv, "root "+dir) || !strings.Contains(srv, "listen "+addr) {
		t.Fatal("the server block no longer has the listen and root lines this test replaces")
	}
	tmp := t.TempDir()
	user := ""
	if os.Geteuid() == 0 {
		user = "user root;\n"
	}
	main := fmt.Sprintf(user+`pid %[1]s/nginx.pid;
error_log %[1]s/error.log;
worker_processes 1;
daemon off;
events { worker_connections 64; }
http {
  access_log off;
  client_body_temp_path %[1]s/body;
  proxy_temp_path %[1]s/proxy;
  fastcgi_temp_path %[1]s/fastcgi;
  uwsgi_temp_path %[1]s/uwsgi;
  scgi_temp_path %[1]s/scgi;
  types { text/html html; application/javascript js; text/css css; }
  default_type application/octet-stream;
  %[2]s
}
`, tmp, srv)
	conf := filepath.Join(tmp, "nginx.conf")
	if err := os.WriteFile(conf, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf, "-p", tmp)
	var out bytes.Buffer
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			logs, _ := os.ReadFile(filepath.Join(tmp, "error.log"))
			t.Logf("nginx error log:\n%s", logs)
		}
	})
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			return "http://" + addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	logs, _ := os.ReadFile(filepath.Join(tmp, "error.log"))
	t.Fatalf("nginx did not start: %s %s", out.String(), logs)
	return ""
}

func TestShowcaseNginxRecipe(t *testing.T) {
	dir, wasm := site(t)
	if err := run([]string{"-brotli", "off", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	base := startNginx(t, dir)
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

	res, body := get("/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/wasm" || res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("main.wasm for gzip: %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Encoding"))
	}
	sibling, err := os.ReadFile(filepath.Join(dir, "main.wasm.gz"))
	if err != nil || !bytes.Equal(body, sibling) {
		t.Errorf("nginx did not send main.wasm.gz as it is (%d bytes against %d): %v", len(body), len(sibling), err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); !bytes.Equal(plain, wasm) {
		t.Error("the compressed body is not the program")
	}
	if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q", res.Header.Get("Vary"))
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache (the file names carry no hash)", cc)
	}

	res, body = get("/main.wasm", map[string]string{"Accept-Encoding": "identity"})
	if res.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, wasm) || res.Header.Get("Content-Type") != "application/wasm" {
		t.Errorf("main.wasm for identity: %q %q, %d bytes", res.Header.Get("Content-Encoding"), res.Header.Get("Content-Type"), len(body))
	}

	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	res, body = get("/main.wasm", map[string]string{"If-None-Match": etag, "Accept-Encoding": "identity"})
	if res.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("a repeat visit: %d with %d bytes, want 304 and none", res.StatusCode, len(body))
	}

	if res, _ = get("/wasm_exec.js", nil); res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Errorf("wasm_exec.js: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res, _ = get("/nothing.js", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("a missing script is %d, want 404", res.StatusCode)
	}
	if res, _ = get("/sw.js", nil); res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("sw.js: %d, Cache-Control %q (it must be fetched afresh to notice a new build)", res.StatusCode, res.Header.Get("Cache-Control"))
	}

	res, body = get("/d/report", map[string]string{"Accept-Encoding": "identity"})
	if res.StatusCode != 200 || !strings.HasPrefix(string(body), "<!doctype html>") {
		t.Errorf("/d/report: %d %.40q", res.StatusCode, body)
	}
}
