package listen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTool writes a shell script that stands in for piper or whisper-cli.
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

func TestWhisperRunsTheBinary(t *testing.T) {
	// whisper-cli -f IN -otxt -of BASE -nt [-m M]: BASE.txt holds the text
	bin := fakeTool(t, `in=$2; base=$5; model=""; [ "$7" = "-m" ] && model=$8
printf '  heard %s with %s \n' "$(wc -c < "$in" | tr -d ' ')" "$model" > "$base.txt"`)
	got, err := Whisper(bin, "m.bin", []byte("12345"))
	if err != nil || got != "heard 5 with m.bin" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWhisperReportsFailures(t *testing.T) {
	if _, err := Whisper(fakeTool(t, `echo "bad model" >&2; exit 2`), "m", []byte("x")); err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("a failing whisper: %v", err)
	}
	if _, err := Whisper(fakeTool(t, `exit 0`), "m", []byte("x")); err == nil {
		t.Fatal("no text file written is an error")
	}
	if _, err := Whisper(filepath.Join(t.TempDir(), "absent"), "m", []byte("x")); err == nil {
		t.Fatal("a missing binary is an error")
	}
}
