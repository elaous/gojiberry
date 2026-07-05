// Package devwatch is the process-supervision half of gox's dev loop:
// watch a source tree, and on a relevant change, rebuild and restart a
// managed server subprocess. Pair it with the sibling devreload package
// for the browser-side half (reloading a tab once the restarted
// process is listening again).
//
// This began as examples/canvas-app's own cmd/devwatch, hardcoded to
// that app's `make generate`/`make wasm`/cmd/devserver specifics; this
// package is that same watch/debounce/rebuild/restart loop generalized
// behind a Config so any gox project's own thin cmd/devwatch (or
// cmd/goxdev, for a project with no Go code of its own to add) can
// reuse it instead of re-deriving fsnotify wiring and subprocess
// lifecycle management from scratch. It is not real hot module
// replacement -- a compiled binary (or .wasm blob) is one opaque unit,
// not a set of hot-swappable modules -- just a fast watch/rebuild/
// restart/reload loop, an acceptable tradeoff for any gox app whose
// server holds no unrecoverable in-memory state a full restart would
// lose (canvas-app's own state lives in Postgres, for instance).
package devwatch

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

// Config configures Run. Root, Rebuild, and Command are required; the
// rest have workable zero values.
type Config struct {
	// Root is the directory tree to watch, recursively.
	Root string

	// Ignore reports whether a changed path should never trigger a
	// rebuild. Combine with DefaultIgnore to keep its universal skips
	// (VCS metadata, generated *.gox.go files) alongside a project's
	// own build-output paths, e.g.:
	//
	//	Ignore: func(p string) bool {
	//		return devwatch.DefaultIgnore(p) || strings.Contains(p, "/static/")
	//	}
	//
	// A nil Ignore is equivalent to DefaultIgnore alone.
	Ignore func(path string) bool

	// Rebuild runs the project's build steps (regenerating .go from
	// .gox, compiling a WASM client, building the server binary this
	// Config's Command runs, ...), streaming their own output as they
	// go. It reports whether the build succeeded; false leaves whatever
	// server is currently running alone rather than restarting it into
	// a known-broken state.
	Rebuild func() bool

	// Command and Args launch the built server binary. Run executes it
	// directly (not through a shell), so a signal sent to stop it
	// reaches the real server process, not an intermediary.
	Command string
	Args    []string

	// Env, if non-nil, replaces the managed process's environment
	// entirely; leave nil to inherit Run's own environment unchanged.
	Env []string
}

// DefaultIgnore reports whether path should never trigger a rebuild
// regardless of project: VCS metadata (.git) and goxc's own generated
// *.gox.go files (edit the .gox source instead -- watching the file
// goxc itself just wrote would create a feedback loop). A project's own
// build-output directories (a WASM static/ dir, a temp binary path, ...)
// are not covered here since there's no universal convention for them;
// combine with Config.Ignore for those.
func DefaultIgnore(path string) bool {
	path = filepath.ToSlash(path)
	switch {
	case strings.Contains(path, "/.git/"), strings.HasPrefix(path, ".git/"):
		return true
	case strings.HasSuffix(path, ".gox.go"):
		return true
	default:
		return false
	}
}

func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// managedProcess owns the single subprocess Run restarts on every
// successful rebuild.
type managedProcess struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	name string
	args []string
	env  []string
}

func (p *managedProcess) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cmd := exec.Command(p.name, p.args...)
	if p.env != nil {
		cmd.Env = p.env
	} else {
		cmd.Env = os.Environ()
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	return nil
}

// stop sends SIGTERM and waits up to 5 seconds before escalating to
// SIGKILL, so a restart never leaves an orphaned process holding the
// port the new one needs.
func (p *managedProcess) stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()
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

// Run performs cfg.Rebuild once, starts the managed process, then
// watches cfg.Root: on a relevant change (debounced 200ms so a burst of
// saves triggers one rebuild, not several), it reruns cfg.Rebuild and,
// if that succeeds, restarts the managed process. It blocks until
// SIGINT/SIGTERM, cleaning up the managed process before returning nil.
// A failed initial build is returned as an error rather than started at
// all, since there would be nothing working yet to fall back to.
func Run(cfg Config) error {
	ignore := cfg.Ignore
	if ignore == nil {
		ignore = DefaultIgnore
	}

	log.Println("devwatch: building...")
	if !cfg.Rebuild() {
		return &buildError{}
	}

	proc := &managedProcess{name: cfg.Command, args: cfg.Args, env: cfg.Env}
	if err := proc.start(); err != nil {
		return err
	}
	defer proc.stop()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := addRecursive(watcher, cfg.Root); err != nil {
		return err
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
				if ignore(ev.Name) {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				// A newly created directory needs its own watch, or
				// files written inside it would go unnoticed.
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
			log.Println("devwatch: rebuilding...")
			if cfg.Rebuild() {
				log.Println("devwatch: restarting")
				proc.stop()
				if err := proc.start(); err != nil {
					log.Printf("devwatch: restarting: %v", err)
				}
			} else {
				log.Println("devwatch: rebuild failed, keeping the previous process running")
			}
		case <-sigCh:
			log.Println("devwatch: shutting down")
			return nil
		}
	}
}

// buildError is returned by Run when the initial build fails, before
// anything is running to fall back to.
type buildError struct{}

func (*buildError) Error() string {
	return "devwatch: initial build failed, fix the error above and re-run"
}
