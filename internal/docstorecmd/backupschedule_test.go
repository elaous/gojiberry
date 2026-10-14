package docstorecmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/docstore"
)

func TestRotationKeepsTheNewestBackupsAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)
	var made []string
	for i := 0; i < 5; i++ {
		n := scheduledName(base.Add(time.Duration(i) * 24 * time.Hour))
		made = append(made, n)
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// files that are not ours, and a directory with a backup's name: never touched
	for _, other := range []string{"notes.txt", "gojiberry-backup-latest.tar", "gojiberry-backup-20260101T000000Z.tar.bak"} {
		if err := os.WriteFile(filepath.Join(dir, other), []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, scheduledName(base.Add(-24*time.Hour))), 0o700); err != nil {
		t.Fatal(err)
	}
	removed, err := rotateBackups(dir, 2)
	if err != nil || removed != 3 {
		t.Fatalf("removed %d: %v", removed, err)
	}
	for i, n := range made {
		_, err := os.Stat(filepath.Join(dir, n))
		if i < 3 && err == nil || i >= 3 && err != nil {
			t.Errorf("%s: %v (the two newest stay)", n, err)
		}
	}
	for _, other := range []string{"notes.txt", "gojiberry-backup-latest.tar", "gojiberry-backup-20260101T000000Z.tar.bak", scheduledName(base.Add(-24 * time.Hour))} {
		if _, err := os.Stat(filepath.Join(dir, other)); err != nil {
			t.Errorf("%s was touched", other)
		}
	}
	if n, err := rotateBackups(dir, 7); err != nil || n != 0 {
		t.Errorf("fewer than keep: %d %v", n, err)
	}
}

func TestBackupIntoADirectoryRunsOnceAndRotates(t *testing.T) {
	ctx := context.Background()
	src := docstore.NewMemoryAdapter()
	_ = src.Save(ctx, "doc", []byte("ciphertext"))
	dir := filepath.Join(t.TempDir(), "backups") // does not exist yet
	// older backups already there
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"gojiberry-backup-20240101T000000Z.tar", "gojiberry-backup-20240102T000000Z.tar"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("old"), 0o600)
	}
	handled, code, out, errOut := run(t, src, "backup", "-dir", dir, "-keep", "2")
	if !handled || code != 0 || !strings.Contains(out, "backed up 1 documents") || !strings.Contains(out, "removed 1 old backups") {
		t.Fatalf("%v %d %q %q", handled, code, out, errOut)
	}
	if !strings.Contains(errOut, `"kind":"backup.made"`) || !strings.Contains(errOut, `"actor":"system"`) || strings.Contains(errOut+out, "doc\"") {
		t.Fatalf("one audit line, no id: %q", errOut)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "gojiberry-backup-*.tar"))
	if len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
	// the newest is a real backup and restores
	dst := docstore.NewMemoryAdapter()
	newest := files[len(files)-1]
	if _, code, _, errOut = run(t, dst, "restore", newest); code != 0 {
		t.Fatalf("restore of %s: %d %q", newest, code, errOut)
	}
}

func TestBackupUsageErrors(t *testing.T) {
	a := docstore.NewMemoryAdapter()
	for _, args := range [][]string{
		{"backup"},
		{"backup", "-every", "1h", "file"},
		{"backup", "-dir", "d", "file"},
		{"backup", "-dir", "d", "-keep", "0"},
		{"backup", "-dir", "d", "-every", "-1h"},
		{"restore", "-dir", "d"},
	} {
		if handled, code, _, errOut := run(t, a, args...); !handled || code != 2 || !strings.Contains(errOut, "usage") && !strings.Contains(errOut, "-keep") {
			t.Errorf("%v: %v %d %q", args, handled, code, errOut)
		}
	}
}

func TestScheduledBackupsRepeatUntilStoppedAndSurviveAFailedRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := docstore.NewMemoryAdapter()
	_ = src.Save(ctx, "doc", []byte("x"))
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	done := make(chan int)
	go func() {
		done <- runScheduledBackups(ctx, src, dir, 3, 20*time.Millisecond, &out, &errOut, audit.NewLogger(&errOut))
	}()
	time.Sleep(250 * time.Millisecond)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code %d", code)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "gojiberry-backup-*.tar"))
	if len(files) == 0 || len(files) > 3 {
		t.Fatalf("%d backups kept, want 1 to 3", len(files))
	}
	// A directory that cannot be made is a failed run, reported, exit 1.
	bad := files[0] // a file where the directory should be
	if code := runScheduledBackups(context.Background(), src, filepath.Join(bad, "sub"), 3, 0, &out, &errOut, audit.NewLogger(&errOut)); code != 1 {
		t.Errorf("unwritable directory: %d", code)
	}
}
