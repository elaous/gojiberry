package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func do(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("opendoc %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestPackListVerifyUnpackSignRecover(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "Reports", "2026"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "notes.md"), []byte("# Notes\n\nhello"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "Reports", "q1.txt"), []byte("first quarter"), 0o644)
	// A .docx made by the library's own exporter.
	reg := registry()
	_, docx, _, err := reg.Export("docx", docfile.Document{Title: "Budget", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "numbers"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "Reports", "2026", "budget.docx"), docx, 0o644)
	_ = os.WriteFile(filepath.Join(src, "junk.bin"), []byte{0xff, 0x00, 0x01}, 0o644)

	key := filepath.Join(dir, "key")
	_ = os.WriteFile(key, []byte(strings.TrimSpace(do(t, "keygen"))+"\n"), 0o600)
	pk := filepath.Join(dir, "work.opendoc")
	if out := do(t, "pack", "-sign", key, "-name", "Work", pk, src); !strings.Contains(out, "3 documents") {
		t.Fatalf("%s", out)
	}
	list := do(t, "list", pk)
	for _, want := range []string{"Work", "3 documents", "2 folders", "signed by", "valid: true", "notes", "q1", "budget", "2026"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}
	if out := do(t, "verify", pk); !strings.Contains(out, "3 documents checked") || strings.Contains(out, "BAD") {
		t.Fatalf("%s", out)
	}
	un := filepath.Join(dir, "un")
	do(t, "unpack", pk, un)
	for _, p := range []string{"notes.open", "Reports/q1.open", "Reports/2026/budget.open"} {
		data, err := os.ReadFile(filepath.Join(un, p))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if _, err := docfile.Open.Unmarshal(data); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	// Sign again with a new key, in place.
	key2 := filepath.Join(dir, "key2")
	_ = os.WriteFile(key2, []byte(strings.TrimSpace(do(t, "keygen"))+"\n"), 0o600)
	do(t, "sign", "-key", key2, pk)
	if out := do(t, "verify", pk); !strings.Contains(out, "valid: true") {
		t.Fatalf("%s", out)
	}
	// A cut pack: verify fails, recover keeps what is whole.
	data, _ := os.ReadFile(pk)
	cutPath := filepath.Join(dir, "cut.opendoc")
	_ = os.WriteFile(cutPath, data[:len(data)-200], 0o644)
	if err := run([]string{"verify", cutPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("a cut pack verified")
	}
	rec := filepath.Join(dir, "rec.opendoc")
	if out := do(t, "recover", cutPath, rec); !strings.Contains(out, "3 documents recovered") {
		t.Fatalf("%s", out)
	}
	if out := do(t, "verify", rec); strings.Contains(out, "BAD") {
		t.Fatalf("%s", out)
	}
}
