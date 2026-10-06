package main

import (
	"os"
	"path/filepath"
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
	// The folders are in the store, found by a second process.
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
