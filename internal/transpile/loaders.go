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
	Decls string
	// GoImports lists additional plain Go imports Decls needs (e.g.
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

// ResolveResourcePath finds the file a resource import names. The path
// is, in order:
//
//   - relative to in.BaseDir (the .xg file's directory), the way go:embed
//     paths are -- "style.css", "./theme/dark.css";
//   - a path in the current module, written as for a Go package:
//     "<module path>/components/button.css";
//   - a path in a module the current one requires (found with `go list
//     -m`, so it works with the module cache, vendoring and replace).
//
// Exported for internal/transpile/experimental's loaders.
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

// loadJSON is implemented in loader_json.go, covering .json/.jsonc/.jsonl
// and the optional TypeName annotation.
//
// CSS used to have its own resource-import loader here (binding a
// plain `"App.css"` import to its file content). It's gone in favor of
// the @stylesheets page-metadata annotation (see page_metadata.go),
// which reads a page component's stylesheet(s) the same way but ties
// them to the function they style instead of a standalone import line.
