package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAgainstAFilesStoreAndAScript(t *testing.T) {
	dir := t.TempDir()
	store := "files:" + filepath.Join(dir, "docs")
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "s.txt")
	if err := os.WriteFile(script, []byte("mkdir Work\nmkdir Work/Reports\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-store", store, "-f", script}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-store", store, "cd", "Work/Reports"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-store", store, "-n", "rmdir", "Work/Reports"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-store", store, "cd", "Work/Reports"}); err != nil {
		t.Fatal("a dry run removed nothing:", err)
	}
	if err := run([]string{"-store", "ftp:nope", "ls"}); err == nil {
		t.Error("an unknown store")
	}
}

func TestScriptFailureExitsNonZeroAndStops(t *testing.T) {
	dir := t.TempDir()
	store := "files:" + filepath.Join(dir, "docs")
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "s.txt")
	if err := os.WriteFile(script, []byte("mkdir A\nrm nothing\nmkdir B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-store", store, "-f", script}); err == nil {
		t.Fatal("a failing script must fail")
	}
	if err := run([]string{"-store", store, "cd", "A"}); err != nil {
		t.Error("what ran before the error stays:", err)
	}
	if err := run([]string{"-store", store, "cd", "B"}); err == nil {
		t.Error("nothing runs after the error")
	}
}

func TestOpenStoreEveryKind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for spec, label := range map[string]string{"": "Memory", "memory": "Memory", "files:" + dir: "Files", "sqlite:" + filepath.Join(dir, "d.db"): "SQLite", "https://docs.example": "Server"} {
		tg, err := openStore(ctx, spec, "")
		if err != nil || tg.Label != label || tg.Adapter == nil {
			t.Errorf("%q: %+v, %v", spec, tg, err)
		}
	}
	if _, err := openStore(ctx, "ftp://nope", ""); err == nil || !strings.Contains(err.Error(), "ftp://nope") {
		t.Errorf("an unknown store is refused and named: %v", err)
	}
}

func TestOpenStoreSendsTheToken(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tg, err := openStore(context.Background(), srv.URL, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tg.Adapter.Load(context.Background(), "x") // the answer does not matter, the header does
	if len(got) == 0 || got[0] != "Bearer s3cret" {
		t.Fatalf("authorization: %v", got)
	}
	got = nil
	tg, _ = openStore(context.Background(), srv.URL, "")
	_, _ = tg.Adapter.Load(context.Background(), "x")
	if len(got) == 0 || got[0] != "" {
		t.Fatalf("no token, no header: %v", got)
	}
}
