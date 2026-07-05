// Command devwatch runs cmd/devserver in DEV=1 mode and rebuilds +
// restarts it whenever a source file changes: watches the canvas-app
// tree (excluding static/, a build-output directory this same rebuild
// writes into, and generated *.gox.go files -- watching either would
// create a feedback loop), then on a change runs `make generate` +
// `make wasm`, builds cmd/devserver to a temp binary, and swaps it in
// for the running one.
//
// The browser side of "reload" is cmd/devserver's own devReloadScript,
// injected only under DEV=1: it polls GET /__dev/version and reloads
// once a new devserver process (a new value, since the version is
// just time.Now() at process start) is listening again.
//
// This is not real hot module replacement -- a compiled .wasm binary
// is one opaque blob, not a set of hot-swappable modules -- just a
// fast watch/rebuild/restart/reload loop. That's an acceptable
// tradeoff for this particular app specifically: it's
// server-source-of-truth (Postgres via canvas.Store), so a full page
// reload always reconstructs the correct state, unlike a client-heavy
// SPA that would lose in-memory state on every rebuild.
//
// Usage (from examples/canvas-app, with Postgres already migrated):
//
//	DATABASE_URL=postgres://... go run ./cmd/devwatch
package main

import (
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ignoredForRebuild reports whether a changed path should never
// trigger a rebuild: build output this same rebuild writes (static/,
// the temp server binary), the generated .gox.go files goxc writes
// (edit the .gox instead), and VCS metadata.
func ignoredForRebuild(path string) bool {
	path = filepath.ToSlash(path)
	switch {
	case strings.Contains(path, "/static/"), strings.HasPrefix(path, "static/"):
		return true
	case strings.Contains(path, "/.git/"), strings.HasPrefix(path, ".git/"):
		return true
	case strings.HasSuffix(path, ".gox.go"):
		return true
	default:
		return false
	}
}

// addRecursive registers root and every subdirectory under it with
// watcher, skipping the same build-output/VCS directories
// ignoredForRebuild excludes files from.
func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if name := d.Name(); name == "static" || name == ".git" {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// runStep runs name with args, streaming its output, and reports
// whether it succeeded.
func runStep(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("devwatch: %s %s failed: %v", name, strings.Join(args, " "), err)
		return false
	}
	return true
}

// devserverBin is where the managed devserver binary is built --
// outside the watched tree (the OS temp dir) so building it can never
// itself trigger a rebuild.
var devserverBin = filepath.Join(os.TempDir(), "gox-canvas-devwatch-server")

// rebuild regenerates .go from .gox, rebuilds the WASM client, and
// builds cmd/devserver to devserverBin. false means something failed
// and its output was already printed; the caller should leave
// whatever devserver is currently running alone.
func rebuild() bool {
	log.Println("devwatch: rebuilding...")
	if !runStep("make", "generate") {
		return false
	}
	if !runStep("make", "wasm") {
		return false
	}
	if !runStep("go", "build", "-o", devserverBin, "./cmd/devserver") {
		return false
	}
	return true
}

// managedServer owns the single devserver subprocess devwatch
// restarts on every successful rebuild. A compiled binary is run
// directly (not `go run ./cmd/devserver`) specifically so stop can
// deliver SIGTERM straight to the real server process instead of
// `go run`'s wrapper, whose signal forwarding to its child is not
// reliable enough for a clean restart.
type managedServer struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (s *managedServer) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd := exec.Command(devserverBin)
	cmd.Env = append(os.Environ(), "DEV=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	return nil
}

func (s *managedServer) stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

func main() {
	defer os.Remove(devserverBin)

	if !rebuild() {
		log.Fatal("devwatch: initial build failed, fix the error above and re-run")
	}

	srv := &managedServer{}
	if err := srv.start(); err != nil {
		log.Fatalf("devwatch: starting devserver: %v", err)
	}
	defer srv.stop()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	if err := addRecursive(watcher, "."); err != nil {
		log.Fatal(err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	trigger := make(chan struct{}, 1)
	var debounce *time.Timer

	go func() {
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ignoredForRebuild(ev.Name) {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				// A newly created directory needs its own watch, or
				// files written inside it will go unnoticed.
				if ev.Op&fsnotify.Create != 0 {
					if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
						addRecursive(watcher, ev.Name)
					}
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, func() {
					select {
					case trigger <- struct{}{}:
					default:
					}
				})
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("devwatch: watch error:", watchErr)
			}
		}
	}()

	log.Println("devwatch: watching for changes (Ctrl-C to stop)")
	for {
		select {
		case <-trigger:
			if rebuild() {
				log.Println("devwatch: restarting devserver")
				srv.stop()
				if err := srv.start(); err != nil {
					log.Printf("devwatch: restarting devserver: %v", err)
				}
			} else {
				log.Println("devwatch: rebuild failed, keeping the previous devserver running")
			}
		case <-sigCh:
			log.Println("devwatch: shutting down")
			return
		}
	}
}
