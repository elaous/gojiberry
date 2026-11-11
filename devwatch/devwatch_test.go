package devwatch

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestDefaultIgnore(t *testing.T) {
	cases := map[string]bool{
		".git/HEAD":               true,
		"foo/.git/index":          true,
		"components/button.xg.go": true,
		"components/button.xg":    false,
		"main.go":                 false,
	}
	for path, want := range cases {
		if got := DefaultIgnore(path); got != want {
			t.Errorf("DefaultIgnore(%q) = %v, want %v", path, got, want)
		}
	}
}

// pollUntil polls cond every 20ms until it holds.
func pollUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never became true within timeout")
}

// readPID reads the PID the managed process wrote.
func readPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// TestRunRebuildsAndRestartsOnRealFileChange runs a real rebuild and restart.
func TestRunRebuildsAndRestartsOnRealFileChange(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "server.pid")

	var rebuilds atomic.Int32
	rebuild := func() bool {
		rebuilds.Add(1)
		return true
	}

	done := make(chan error, 1)
	go func() {
		done <- Run(Config{
			Root:    dir,
			Rebuild: rebuild,
			Command: "sh",
			Args:    []string{"-c", "echo $$ > " + pidFile + "; exec sleep 100"},
		})
	}()

	// Wait for the initial build + process start.
	pollUntil(t, 2*time.Second, func() bool { return rebuilds.Load() >= 1 })
	pollUntil(t, 2*time.Second, func() bool { return readPID(pidFile) != 0 })
	firstPID := readPID(pidFile)

	if err := os.WriteFile(filepath.Join(dir, "changed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing to watched dir: %v", err)
	}

	pollUntil(t, 3*time.Second, func() bool { return rebuilds.Load() >= 2 })
	pollUntil(t, 3*time.Second, func() bool {
		pid := readPID(pidFile)
		return pid != 0 && pid != firstPID
	})

	rebuildsBeforeIgnored := rebuilds.Load()
	if err := os.WriteFile(filepath.Join(dir, "generated.xg.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing ignored file: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // long enough for the 200ms debounce to have fired if it was going to
	if got := rebuilds.Load(); got != rebuildsBeforeIgnored {
		t.Errorf("a change to a *.xg.go file triggered a rebuild (count %d -> %d), want no change", rebuildsBeforeIgnored, got)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signaling self: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned an error after shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}
}

func TestEditorScratchFilesAreIgnored(t *testing.T) {
	for _, p := range []string{"src/.main.go.swp", "src/main.go~", "src/4913", "src/.#main.go", "x/a.tmp"} {
		if !DefaultIgnore(p) {
			t.Errorf("%s should be ignored", p)
		}
	}
	if DefaultIgnore("src/main.go") {
		t.Error("source files must not be ignored")
	}
}

func TestAddRecursiveSkipsHeavyDirsAndSurvivesUnreadableOnes(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src/pkg", "node_modules/dep", ".git/objects"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := addRecursive(w, root); err != nil {
		t.Fatal(err)
	}
	for _, p := range w.WatchList() {
		if strings.Contains(p, "node_modules") || strings.Contains(p, ".git") {
			t.Errorf("watching %s", p)
		}
	}
	if len(w.WatchList()) != 3 { // root, src, src/pkg
		t.Errorf("watch list %v", w.WatchList())
	}
}

func TestRunStopsAtOnceWhenTheFirstBuildFails(t *testing.T) {
	err := Run(Config{Root: t.TempDir(), Rebuild: func() bool { return false }, Command: "sh", Args: []string{"-c", "exit 0"}})
	if err == nil || !strings.Contains(err.Error(), "initial build failed") {
		t.Fatalf("got %v", err)
	}
	var be *buildError
	if !errors.As(err, &be) {
		t.Errorf("the error is a build error: %T", err)
	}
}

func TestRunReportsAProcessThatCannotStart(t *testing.T) {
	err := Run(Config{Root: t.TempDir(), Rebuild: func() bool { return true }, Command: filepath.Join(t.TempDir(), "no-such-binary")})
	if err == nil {
		t.Fatal("a command that does not exist is an error")
	}
}

func TestARebuildThatFailsKeepsTheRunningProcess(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "server.pid")
	var rebuilds atomic.Int32
	rebuild := func() bool { return rebuilds.Add(1) == 1 } // the first build works, later ones fail
	done := make(chan error, 1)
	go func() {
		done <- Run(Config{Root: dir, Rebuild: rebuild, Command: "sh", Args: []string{"-c", "echo $$ > " + pidFile + "; exec sleep 100"}})
	}()
	pollUntil(t, 2*time.Second, func() bool { return readPID(pidFile) != 0 })
	pid := readPID(pidFile)
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 3*time.Second, func() bool { return rebuilds.Load() >= 2 })
	time.Sleep(300 * time.Millisecond) // time for a restart, had there been one
	if readPID(pidFile) != pid {
		t.Error("the process was restarted after a failed build")
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}
}
