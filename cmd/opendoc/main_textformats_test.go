//go:build experimental || exp_textformats

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Text formats are packed as documents too.
func TestPackTakesTextFormats(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "sales.csv"), []byte("item,qty\npens,3\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "config.yaml"), []byte("name: demo\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "data.json"), []byte(`{"users":[{"id":1}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(src, "binary.log"), []byte{'a', 0, 'b'}, 0o644)
	pk := filepath.Join(dir, "t.opendoc")
	out := do(t, "pack", pk, src)
	if !strings.Contains(out, "3 documents") {
		t.Fatalf("%s", out)
	}
	list := do(t, "list", pk)
	for _, want := range []string{"sales", "config", "data"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}
}
