package transpile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// loaderInput is what a resource-import loader needs to do its work.
type loaderInput struct {
	Alias      string // the bound identifier: the import's alias, or one derived from the filename
	Path       string // the import path exactly as written in the .gox source
	BaseDir    string // the .gox file's directory; resource paths resolve relative to it, like go:embed
	SourceFile string // the .gox file's path, for error messages
}

// loaderResult is what a loader hands back to be spliced into the
// generated file in place of the resource import.
type loaderResult struct {
	// Decls is Go source (types/vars/funcs) inserted right after the
	// import block, for loaders that splice code directly into the
	// importing file (JSON, CSS/SCSS).
	Decls string
	// GoImports lists additional plain Go imports Decls needs (e.g.
	// "encoding/json"), merged into the rewritten import block.
	GoImports []string
	// ImportPath, if non-empty, means this loader generated a real Go
	// package (HCL/proto/capnp) and the resource import should become
	// an ordinary `Alias "ImportPath"` import instead of inline Decls.
	ImportPath string
}

type loaderFunc func(loaderInput) (loaderResult, error)

// loaders maps a resource file extension to the function that handles
// importing it. Adding a new importable format is adding an entry here.
var loaders = map[string]loaderFunc{
	".json":  loadJSON,
	".css":   loadCSS,
	".scss":  loadSCSS,
	".hcl":   loadHCL,
	".proto": loadProto,
	".capnp": loadCapnProto,
}

// isResourceImport reports whether path names a file this package knows
// how to import directly, rather than a Go package import path.
func isResourceImport(path string) bool {
	_, ok := loaders[strings.ToLower(filepath.Ext(path))]
	return ok
}

// deriveIdent turns a resource's base filename into a valid Go
// identifier, used when the import spec has no explicit alias:
// "App.css" -> "App_css", "config.json" -> "config_json".
func deriveIdent(path string) string {
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

// resolveResourcePath resolves in.Path relative to in.BaseDir (the
// same convention go:embed uses: relative to the source file's
// directory, not the module root or a fetched URL) and confirms it
// exists.
func resolveResourcePath(in loaderInput) (string, error) {
	abs := filepath.Join(in.BaseDir, in.Path)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("gox: resource import %q in %s: %w (resolved to %s)", in.Path, in.SourceFile, err, abs)
	}
	return abs, nil
}

// loadJSON binds Alias to the decoded JSON value (as `any`) and
// AliasBytes to the raw embedded content, so callers needing a
// concrete type can json.Unmarshal(AliasBytes, &myStruct) themselves;
// gox doesn't infer a struct shape from sample JSON data.
func loadJSON(in loaderInput) (loaderResult, error) {
	abs, err := resolveResourcePath(in)
	if err != nil {
		return loaderResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return loaderResult{}, fmt.Errorf("gox: JSON import %q: %w", in.Path, err)
	}
	var decl strings.Builder
	fmt.Fprintf(&decl, "var %sBytes = []byte(%s)\n", in.Alias, strconv.Quote(string(data)))
	fmt.Fprintf(&decl, "var %s any = func() any {\n\tvar v any\n\tif err := json.Unmarshal(%sBytes, &v); err != nil {\n\t\tpanic(%s + err.Error())\n\t}\n\treturn v\n}()\n",
		in.Alias, in.Alias, strconv.Quote("gox: invalid embedded JSON "+in.Path+": "))
	return loaderResult{Decls: decl.String(), GoImports: []string{"encoding/json"}}, nil
}

// loadCSS binds Alias to the file's content as a plain Go string.
func loadCSS(in loaderInput) (loaderResult, error) {
	abs, err := resolveResourcePath(in)
	if err != nil {
		return loaderResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return loaderResult{}, fmt.Errorf("gox: CSS import %q: %w", in.Path, err)
	}
	return loaderResult{Decls: fmt.Sprintf("var %s = %s\n", in.Alias, strconv.Quote(string(data)))}, nil
}

// loadSCSS compiles the file with the `sass` (dart-sass) CLI and binds
// Alias to the compiled CSS as a plain Go string. Requires `sass` on
// PATH; gox does not bundle or reimplement a SCSS compiler.
func loadSCSS(in loaderInput) (loaderResult, error) {
	abs, err := resolveResourcePath(in)
	if err != nil {
		return loaderResult{}, err
	}
	tool, lookErr := exec.LookPath("sass")
	if lookErr != nil {
		return loaderResult{}, fmt.Errorf("gox: SCSS import %q requires the `sass` (dart-sass) compiler on PATH; install it from https://sass-lang.com/install", in.Path)
	}
	out, err := exec.Command(tool, abs).Output()
	if err != nil {
		return loaderResult{}, fmt.Errorf("gox: compiling %q with sass: %w", in.Path, err)
	}
	return loaderResult{Decls: fmt.Sprintf("var %s = %s\n", in.Alias, strconv.Quote(string(out)))}, nil
}
