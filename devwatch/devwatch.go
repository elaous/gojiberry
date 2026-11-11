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

// Config configures Run.
type Config struct {
	// Root is the directory tree to watch.
	Root string

	// Ignore reports whether a changed path should be skipped.
	Ignore func(path string) bool

	// Rebuild runs the project's build steps before each restart.
	Rebuild func() bool

	// Command and Args launch the built server.
	Command string
	Args    []string

	// Env, if non-nil, replaces the managed process environment.
	Env []string
}

// DefaultIgnore reports whether path should never be watched.
func DefaultIgnore(path string) bool {
	path = filepath.ToSlash(path)
	switch {
	case strings.Contains(path, "/.git/"), strings.HasPrefix(path, ".git/"):
		return true
	case strings.HasSuffix(path, ".xg.go"):
		return true
	case isEditorTemp(filepath.Base(path)):
		return true
	default:
		return false
	}
}

func isEditorTemp(name string) bool {
	switch {
	case name == "4913", strings.HasPrefix(name, ".#"), strings.HasSuffix(name, "~"),
		strings.HasSuffix(name, ".swp"), strings.HasSuffix(name, ".swx"), strings.HasSuffix(name, ".tmp"):
		return true
	}
	return false
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "target": true, ".hg": true, ".svn": true}

func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// one unreadable entry must not stop the whole watch
			log.Printf("devwatch: not watching %s: %v", path, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

type managedProcess struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{} // closed once cmd has exited and been waited for
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
	done := make(chan struct{})
	p.done = done
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		current := p.cmd == cmd
		p.mu.Unlock()
		if current { // Only one stop() tears the process down.
			log.Printf("devwatch: the process exited (%v); it restarts after the next successful rebuild", err)
		}
		close(done)
	}()
	return nil
}

// SIGTERM, SIGKILL after 5s: never orphan the port holder
func (p *managedProcess) stop() {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.cmd, p.done = nil, nil
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM) // best-effort -- the process may already be gone
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

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
	defer func() { _ = watcher.Close() }()
	if err := addRecursive(watcher, cfg.Root); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

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
				// a new directory needs its own watch
				if ev.Op&fsnotify.Create != 0 {
					if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
						_ = addRecursive(watcher, ev.Name) // a failure here just means that one subdirectory goes unwatched
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

type buildError struct{}

func (*buildError) Error() string {
	return "devwatch: initial build failed, fix the error above and re-run"
}
