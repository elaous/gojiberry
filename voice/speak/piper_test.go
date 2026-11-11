package speak

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeTool(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPiperRunsTheBinary(t *testing.T) {
	bin := fakeTool(t, `out=$2; model=""; [ "$3" = "--model" ] && model=$4
printf 'RIFF[%s][%s]' "$(cat)" "$model" > "$out"`)
	got, err := Piper(bin, "voice.onnx", "hello")
	if err != nil || string(got) != "RIFF[hello][voice.onnx]" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := Piper(bin, "", "hi"); err != nil || string(got) != "RIFF[hi][]" {
		t.Fatalf("no model: %q, %v", got, err)
	}
}

func TestPiperReportsFailures(t *testing.T) {
	bin := fakeTool(t, `echo "no such voice" >&2; exit 3`)
	if _, err := Piper(bin, "x", "hi"); err == nil || !strings.Contains(err.Error(), "no such voice") {
		t.Fatalf("a failing piper: %v", err)
	}
	if _, err := Piper(filepath.Join(t.TempDir(), "absent"), "x", "hi"); err == nil {
		t.Fatal("a missing binary is an error")
	}
}
