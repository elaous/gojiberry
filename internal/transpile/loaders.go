package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// LoaderInput is what a resource-import loader needs to do its work.
// Exported so internal/transpile/experimental can implement loaders of
// type LoaderFunc without living in this package.
type LoaderInput struct {
	Alias      string // the bound identifier: the import's alias, or one derived from the filename
	TypeName   string // set from an optional `alias.(TypeName) "path"` annotation; "" if not given
	Path       string // the import path exactly as written in the .gox source
	BaseDir    string // the .gox file's directory; resource paths resolve relative to it, like go:embed
	SourceFile string // the .gox file's path, for error messages
}

// LoaderResult is what a loader hands back to be spliced into the
// generated file in place of the resource import.
type LoaderResult struct {
	// Decls is Go source (types/vars/funcs) inserted right after the
	// import block, for loaders that splice code directly into the
	// importing file (JSON, CSS/SCSS).
	Decls string
	// GoImports lists additional plain Go imports Decls needs (e.g.
	// "encoding/json"), merged into the rewritten import block.
	GoImports []string
	// ImportPath, if non-empty, means this loader generated a real Go
	// package (HCL/proto/capnp/sqlc) and the resource import should
	// become an ordinary `Alias "ImportPath"` import instead of inline
	// Decls.
	ImportPath string
}

// LoaderFunc is the signature every resource-import loader implements,
// stable or experimental.
type LoaderFunc func(LoaderInput) (LoaderResult, error)

// typeAnnotationAware lists the extensions whose loader understands
// TypeName. isResourceImport/rewriteImportBlock let any resource import
// carry the annotation syntax, but only these loaders know what to do
// with it; anything else naming one is a mistake worth surfacing.
var typeAnnotationAware = map[string]bool{".json": true, ".jsonc": true, ".jsonl": true}

// loaders maps a resource file extension to the function that handles
// importing it -- the stable, on-by-default set. Adding a new
// importable format to the stable tier is adding an entry here; risky
// or reimplements-a-slice-of-a-mature-tool formats belong in
// internal/transpile/experimental instead (see experimentalExtensions).
var loaders = map[string]LoaderFunc{
	".json":  loadJSON,
	".jsonc": loadJSON,
	".jsonl": loadJSON,
}

// experimentalExtensions are resource-import extensions gox recognizes
// but whose loader lives in internal/transpile/experimental and isn't
// wired in unless Options.Experimental is set. Listing them here (even
// without an implementation loaded) lets gox give an actionable "this
// needs experimental mode" error instead of a confusing "not a valid
// Go import path" one when they're used without opting in.
var experimentalExtensions = map[string]bool{
	".hcl":   true,
	".proto": true,
	".capnp": true,
	".sql":   true,
}

// isResourceImport reports whether path names a file gox recognizes as
// an import-time resource (stable or experimental), rather than an
// ordinary Go package import path.
func isResourceImport(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return loaders[ext] != nil || experimentalExtensions[ext]
}

// DeriveIdent turns a resource's base filename into a valid Go
// identifier, used when the import spec has no explicit alias:
// "App.css" -> "App_css", "config.json" -> "config_json". Exported so
// experimental loaders can use the same convention.
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

// ResolveResourcePath resolves in.Path relative to in.BaseDir (the
// same convention go:embed uses: relative to the source file's
// directory, not the module root or a fetched URL) and confirms it
// exists. Exported for internal/transpile/experimental's loaders.
func ResolveResourcePath(in LoaderInput) (string, error) {
	abs := filepath.Join(in.BaseDir, in.Path)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("gox: resource import %q in %s: %w (resolved to %s)", in.Path, in.SourceFile, err, abs)
	}
	return abs, nil
}

// FindModuleRoot is module.go's findModuleRoot, exported for
// internal/transpile/experimental's loaders (HCL/proto/capnp/sqlc all
// generate a real Go subpackage and need to know the module root/path
// to place and name it).
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
