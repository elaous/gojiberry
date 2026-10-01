package transpile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const goxImportPath = "github.com/elaous/gojiberry/gox"

// loadCSS handles .css imports, written like a Go package import with a
// module-qualified path:
//
//	import (
//		_ "github.com/me/app/components/button.css"
//		theme "github.com/me/app/theme/dark.css"
//	)
//
// A blank import registers the stylesheet with the program, the way a
// blank import of a Go package runs its init: the generated file gets
//
//	func init() { gox.RegisterStyle("github.com/me/app/components/button.css", "...") }
//
// so whoever imports the package gets the styles -- gox.Document puts
// every registered sheet in the page's head, and domrt.Mount installs
// them in the browser's. A named import only binds the file's content to
// a constant (`const theme = "..."`), for code that wants to place it
// itself.
func loadCSS(in LoaderInput) (LoaderResult, error) {
	if !in.AliasGiven {
		return LoaderResult{}, fmt.Errorf("gox: CSS import %q in %s needs a name or _ in front: `import _ %q` registers the stylesheet, `import name %q` binds its text", in.Path, in.SourceFile, in.Path, in.Path)
	}
	abs, err := ResolveResourcePath(in)
	if err != nil {
		return LoaderResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return LoaderResult{}, fmt.Errorf("gox: CSS import %q: %w", in.Path, err)
	}
	if in.Alias != "_" {
		return LoaderResult{Decls: fmt.Sprintf("const %s = %s\n", in.Alias, strconv.Quote(string(data)))}, nil
	}
	return LoaderResult{
		Decls:     fmt.Sprintf("func init() { gox.RegisterStyle(%s, %s) }\n", strconv.Quote(styleID(in, abs)), strconv.Quote(string(data))),
		GoImports: []string{goxImportPath},
	}, nil
}

// styleID is the stable name a registered sheet goes by: its
// module-qualified path, whether it was written that way or relative to
// the .gox file.
func styleID(in LoaderInput, abs string) string {
	root, modPath, err := findModuleRoot(in.BaseDir)
	if err == nil {
		if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return modPath + "/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(in.Path)
}
