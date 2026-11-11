package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type LoaderInput struct {
	Alias      string
	AliasGiven bool
	TypeName   string
	Path       string // the import path exactly as written in the .xg source
	BaseDir    string // the .xg file's directory; resource paths resolve relative to it, like go:embed
	SourceFile string // the .xg file's path, for error messages
}

type LoaderResult struct {
	Decls      string
	GoImports  []string
	ImportPath string
}

type LoaderFunc func(LoaderInput) (LoaderResult, error)

var typeAnnotationAware = map[string]bool{".json": true, ".jsonc": true, ".jsonl": true}

var loaders = map[string]LoaderFunc{
	".json":  loadJSON,
	".jsonc": loadJSON,
	".jsonl": loadJSON,
	".css":   loadCSS,
}

var experimentalExtensions = map[string]bool{
	".hcl":   true,
	".proto": true,
	".capnp": true,
	".sql":   true,
}

func isResourceImport(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return loaders[ext] != nil || experimentalExtensions[ext]
}

func DeriveIdent(path string) string {
	base := filepath.Base(path)
	var b strings.Builder
	for i, r := range base {
		if unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" || unicode.IsDigit(rune(s[0])) {
		s = "_" + s
	}
	return s
}

// ResolveResourcePath tries BaseDir, the current module, then required modules.
func ResolveResourcePath(in LoaderInput) (string, error) {
	rel := filepath.Join(in.BaseDir, in.Path)
	if _, err := os.Stat(rel); err == nil {
		return rel, nil
	}
	if abs, ok := resolveInModules(in.BaseDir, in.Path); ok {
		return abs, nil
	}
	return "", fmt.Errorf("xango: resource import %q in %s: no such file relative to %s, in this module, or in a required module", in.Path, in.SourceFile, in.BaseDir)
}

func FindModuleRoot(startDir string) (dir string, modulePath string, err error) {
	return findModuleRoot(startDir)
}

// loadJSON lives in loader_json.go; CSS uses the @stylesheets annotation.
