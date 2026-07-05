package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var moduleLineRe = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

// findModuleRoot walks up from startDir looking for a go.mod, returning
// the directory that contains it and the module path it declares. Used
// by loaders (HCL/proto/capnp) that generate a real Go subpackage and
// need to know both where to write it and what import path to give it.
func findModuleRoot(startDir string) (dir string, modulePath string, err error) {
	dir = startDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(goModPath); err == nil {
			m := moduleLineRe.FindSubmatch(data)
			if m == nil {
				return "", "", fmt.Errorf("gox: %s has no `module` line", goModPath)
			}
			return dir, string(m[1]), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("gox: no go.mod found above %s", startDir)
		}
		dir = parent
	}
}
