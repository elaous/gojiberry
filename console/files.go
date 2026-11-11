package console

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Files interface {
	// Glob lists the files a pattern names.
	Glob(pattern string) ([]string, error)
	// ReadFile returns a file's bytes.
	ReadFile(name string) ([]byte, error)
	// Exists says a file is there.
	Exists(name string) bool
	// MkdirAll makes a folder to write into.
	MkdirAll(dir string) error
	Write(name string, force bool, write func(io.Writer) error) error
}

func (sh *Shell) files() Files {
	if sh.Files != nil {
		return sh.Files
	}
	return OSFiles{}
}

// OSFiles is Files on the disk.
type OSFiles struct{}

// Glob implements Files.
func (OSFiles) Glob(pattern string) ([]string, error) {
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		matches = []string{pattern}
	}
	return matches, nil
}

// ReadFile implements Files.
func (OSFiles) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// Exists implements Files.
func (OSFiles) Exists(name string) bool { _, err := os.Stat(name); return err == nil }

// MkdirAll implements Files.
func (OSFiles) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

// Write implements Files, via a temporary file.
func (OSFiles) Write(name string, force bool, write func(io.Writer) error) (err error) {
	if !force {
		if _, serr := os.Stat(name); serr == nil {
			return fmt.Errorf("%s exists (use --force to replace it)", name)
		}
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".docsh-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	if err = write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

type PageFiles struct {
	// Deliver hands over a finished file.
	Deliver func(name string, data []byte)

	mu        sync.Mutex
	inbox     map[string][]byte
	delivered map[string]bool
}

// Add puts a picked file in the inbox.
func (p *PageFiles) Add(name string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inbox == nil {
		p.inbox = map[string][]byte{}
	}
	p.inbox[path.Base(name)] = data
}

// Names lists the files in the inbox, sorted.
func (p *PageFiles) Names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.inbox))
	for n := range p.inbox {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Clear empties the inbox.
func (p *PageFiles) Clear() {
	p.mu.Lock()
	p.inbox = nil
	p.mu.Unlock()
}

// Glob implements Files for the inbox names.
func (p *PageFiles) Glob(pattern string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for n := range p.inbox {
		if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(n)); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		out = []string{pattern}
	}
	return out, nil
}

// ReadFile implements Files.
func (p *PageFiles) ReadFile(name string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d, ok := p.inbox[path.Base(name)]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("%s: no such file here (add files with the Add files button first)", name)
}

func (p *PageFiles) Exists(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.delivered[name]
}

// MkdirAll implements Files; the inbox has no folders.
func (*PageFiles) MkdirAll(string) error { return nil }

func (p *PageFiles) Write(name string, force bool, write func(io.Writer) error) error {
	if p.Deliver == nil {
		return errors.New("console: nowhere to deliver the file")
	}
	if !force && p.Exists(name) {
		return fmt.Errorf("%s exists (use --force to replace it)", name)
	}
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		return err
	}
	p.mu.Lock()
	if p.delivered == nil {
		p.delivered = map[string]bool{}
	}
	p.delivered[name] = true
	p.mu.Unlock()
	p.Deliver(name, buf.Bytes())
	return nil
}
