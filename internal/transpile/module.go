package transpile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var moduleLineRe = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

func findModuleRoot(startDir string) (dir string, modulePath string, err error) {
	dir = startDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(goModPath); err == nil {
			m := moduleLineRe.FindSubmatch(data)
			if m == nil {
				return "", "", fmt.Errorf("xango: %s has no `module` line", goModPath)
			}
			return dir, string(m[1]), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("xango: no go.mod found above %s", startDir)
		}
		dir = parent
	}
}

func resolveInModules(baseDir, path string) (string, bool) {
	root, modPath, err := findModuleRoot(baseDir)
	if err != nil {
		return "", false
	}
	if rest, ok := strings.CutPrefix(path, modPath+"/"); ok {
		abs := filepath.Join(root, filepath.FromSlash(rest))
		if _, err := os.Stat(abs); err == nil {
			return abs, true
		}
		return "", false
	}
	// the longest module path that prefixes path
	var best, bestDir string
	for _, m := range requiredModules(root) {
		if m.Dir != "" && strings.HasPrefix(path, m.Path+"/") && len(m.Path) > len(best) {
			best, bestDir = m.Path, m.Dir
		}
	}
	if best == "" {
		return "", false
	}
	abs := filepath.Join(bestDir, filepath.FromSlash(strings.TrimPrefix(path, best+"/")))
	if _, err := os.Stat(abs); err != nil {
		return "", false
	}
	return abs, true
}

type goModule struct{ Path, Dir string }

var (
	modulesMu    sync.Mutex
	modulesCache = map[string][]goModule{}
)

func requiredModules(root string) []goModule {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	if ms, ok := modulesCache[root]; ok {
		return ms
	}
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = root
	out, err := cmd.Output()
	var ms []goModule
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			var m goModule
			if err := dec.Decode(&m); err == io.EOF || err != nil {
				break
			}
			ms = append(ms, m)
		}
	}
	modulesCache[root] = ms
	return ms
}
