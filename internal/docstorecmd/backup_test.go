package docstorecmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func run(t *testing.T, a docstore.Adapter, args ...string) (handled bool, code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	handled, code = runOperatorCommand(context.Background(), a, args, &o, &e)
	return handled, code, o.String(), e.String()
}

func TestBackupThenRestoreCommands(t *testing.T) {
	ctx := context.Background()
	src := docstore.NewMemoryAdapter()
	_ = src.Save(ctx, "private-report", []byte("ciphertext bytes"))
	_ = src.Save(ctx, "other", []byte("more"))
	file := filepath.Join(t.TempDir(), "store.backup")

	handled, code, out, errOut := run(t, src, "backup", file)
	if !handled || code != 0 || !strings.Contains(out, "backed up 2 documents") {
		t.Fatalf("%v %d %q %q", handled, code, out, errOut)
	}
	if strings.Contains(out+errOut, "private-report") {
		t.Fatal("no id may appear in the output or the audit line")
	}
	if !strings.Contains(errOut, `"kind":"backup.made"`) || !strings.Contains(errOut, `"count":2`) {
		t.Fatalf("one audit line: %q", errOut)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the backup is private to its owner: %v %v", st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".backup-*")); len(left) != 0 {
		t.Fatalf("temp file left: %v", left)
	}

	dst := docstore.NewMemoryAdapter()
	handled, code, out, errOut = run(t, dst, "restore", file)
	if !handled || code != 0 || !strings.Contains(out, "restored 2 documents") || !strings.Contains(errOut, `"kind":"backup.restored"`) {
		t.Fatalf("%v %d %q %q", handled, code, out, errOut)
	}
	if d, err := dst.Load(ctx, "private-report"); err != nil || string(d.Data) != "ciphertext bytes" {
		t.Fatalf("%q %v", d.Data, err)
	}

	_, code, _, errOut = run(t, dst, "restore", file)
	if code != 1 || !strings.Contains(errOut, "-force") || !strings.Contains(errOut, `"reason":"conflict"`) {
		t.Fatalf("%d %q", code, errOut)
	}
	if _, code, _, _ = run(t, dst, "restore", "-force", file); code != 0 {
		t.Fatalf("-force: %d", code)
	}
}

func TestOperatorCommandErrors(t *testing.T) {
	a := docstore.NewMemoryAdapter()
	if handled, _, _, _ := run(t, a); handled {
		t.Fatal("no arguments is the server")
	}
	if handled, _, _, _ := run(t, a, "serve"); handled {
		t.Fatal("other words are not ours")
	}
	for _, args := range [][]string{{"backup"}, {"restore"}, {"backup", "a", "b"}, {"backup", "-force", "x"}, {"restore", "-nope", "x"}} {
		if handled, code, _, errOut := run(t, a, args...); !handled || code != 2 || !strings.Contains(errOut, "usage:") {
			t.Errorf("%v: %v %d %q", args, handled, code, errOut)
		}
	}
	dir := t.TempDir()
	if _, code, _, errOut := run(t, a, "restore", filepath.Join(dir, "missing")); code != 1 || !strings.Contains(errOut, "restore failed") {
		t.Fatalf("%d %q", code, errOut)
	}
	bad := filepath.Join(dir, "bad")
	_ = os.WriteFile(bad, []byte("not a backup, long enough to be read as a tar header block........................................................................................................................................................................................................................................................................................................................................................................................................................"), 0o600)
	if _, code, _, errOut := run(t, a, "restore", bad); code != 1 || !strings.Contains(errOut, `"outcome":"error"`) {
		t.Fatalf("%d %q", code, errOut)
	}
	if _, code, _, errOut := run(t, a, "backup", filepath.Join(dir, "no", "such", "dir", "f")); code != 1 || !strings.Contains(errOut, "backup failed") {
		t.Fatalf("%d %q", code, errOut)
	}
}
