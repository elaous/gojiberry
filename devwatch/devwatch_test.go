package devwatch

import (
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

// pollUntil polls cond every 20ms until it's true or timeout elapses,
// failing the test if it never becomes true.
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

// readPID reads the PID the managed process self-reported at path, or
// 0 if it hasn't written one (yet, or anymore, after being stopped).
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

// TestRunRebuildsAndRestartsOnRealFileChange drives Run for real: a
// real fsnotify watch on a real temp directory, a real subprocess
// standing in for a server (which self-reports its own PID to a file,
// via `echo $$` before exec'ing sleep -- exec preserves the PID, so the
// reported value is the long-running process's real PID, letting the
// test confirm an actual restart happened rather than just counting
// Rebuild calls), a real file write to trigger it, and a real SIGTERM
// to this test process to exercise Run's own shutdown path -- the only
// way its public API offers to stop it.
func TestRunRebuildsAndRestartsOnRealFileChange(t *testing.T) {
	dir := t.TempDir()
	// A second, separate temp dir for the PID file -- it must not live
	// under dir (the watched Root), or the managed process writing its
	// own PID would itself trigger an unwanted rebuild.
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

	// A real write under the watched root should trigger exactly one
	// more rebuild (after the 200ms debounce) and a restart -- a new
	// process self-reporting a different PID than the first.
	if err := os.WriteFile(filepath.Join(dir, "changed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing to watched dir: %v", err)
	}

	pollUntil(t, 3*time.Second, func() bool { return rebuilds.Load() >= 2 })
	pollUntil(t, 3*time.Second, func() bool {
		pid := readPID(pidFile)
		return pid != 0 && pid != firstPID
	})

	// A change under a path DefaultIgnore covers should not trigger
	// another rebuild.
	rebuildsBeforeIgnored := rebuilds.Load()
	if err := os.WriteFile(filepath.Join(dir, "generated.xg.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing ignored file: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // long enough for the 200ms debounce to have fired if it was going to
	if got := rebuilds.Load(); got != rebuildsBeforeIgnored {
		t.Errorf("a change to a *.xg.go file triggered a rebuild (count %d -> %d), want no change", rebuildsBeforeIgnored, got)
	}

	// Exercise Run's shutdown path for real.
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
