package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const xangoImportPath = "github.com/elaous/gojiberry/xango"

func loadCSS(in LoaderInput) (LoaderResult, error) {
	if !in.AliasGiven {
		return LoaderResult{}, fmt.Errorf("xango: CSS import %q in %s needs a name or _ in front: `import _ %q` registers the stylesheet, `import name %q` binds its text", in.Path, in.SourceFile, in.Path, in.Path)
	}
	abs, err := ResolveResourcePath(in)
	if err != nil {
		return LoaderResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return LoaderResult{}, fmt.Errorf("xango: CSS import %q: %w", in.Path, err)
	}
	if in.Alias != "_" {
		return LoaderResult{Decls: fmt.Sprintf("const %s = %s\n", in.Alias, strconv.Quote(string(data)))}, nil
	}
	return LoaderResult{
		Decls:     fmt.Sprintf("func init() { xango.RegisterStyle(%s, %s) }\n", strconv.Quote(styleID(in, abs)), strconv.Quote(string(data))),
		GoImports: []string{xangoImportPath},
	}, nil
}

func styleID(in LoaderInput, abs string) string {
	root, modPath, err := findModuleRoot(in.BaseDir)
	if err == nil {
		if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return modPath + "/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(in.Path)
}
