package transpile

import (
	"fmt"
	"path/filepath"
	"strings"
)

// processImports runs before tag expansion: it finds every
// `import ( ... )` block, and for each spec whose path ends in a
// recognized resource extension invokes that extension's loader
// instead of treating it as a Go package import. A loader either
// splices supporting Go declarations in after the import block (JSON/
// CSS/SCSS) or generates a real Go package and rewrites the spec into
// an ordinary import of it (the experimental loaders: HCL/proto/
// capnp/sqlc). Ordinary Go import specs pass through unchanged. Only
// the parenthesized `import ( ... )` form is recognized; a lone
// `import "path"` is always treated as a normal Go import.
func processImports(filename, baseDir string, src string, opts Options) (string, error) {
	runes := []rune(src)
	var out []rune
	pos := 0
	n := len(runes)
	for pos < n {
		if end, ok := skipStringLiteral(runes, pos); ok {
			out = append(out, runes[pos:end]...)
			pos = end
			continue
		}
		if end, ok := skipComment(runes, pos); ok {
			out = append(out, runes[pos:end]...)
			pos = end
			continue
		}
		if atWord(runes, pos, "import") {
			afterWord := pos + len("import")
			i := afterWord
			for i < n && isSpace(runes[i]) {
				i++
			}
			if i < n && runes[i] == '(' {
				block, decls, newPos, err := rewriteImportBlock(filename, baseDir, runes, i+1, opts)
				if err != nil {
					return "", err
				}
				out = append(out, runes[pos:i]...) // "import" + whitespace
				out = append(out, '(')
				out = append(out, []rune(block)...)
				out = append(out, ')', '\n')
				out = append(out, []rune(decls)...)
				pos = newPos
				continue
			}
		}
		out = append(out, runes[pos])
		pos++
	}
	return string(out), nil
}

// atWord reports whether src[pos:] begins with word as a standalone
// token (not part of a longer identifier on either side).
func atWord(src []rune, pos int, word string) bool {
	wr := []rune(word)
	if pos+len(wr) > len(src) {
		return false
	}
	for i, r := range wr {
		if src[pos+i] != r {
			return false
		}
	}
	if pos > 0 && isIdentPart(src[pos-1]) {
		return false
	}
	end := pos + len(wr)
	if end < len(src) && isIdentPart(src[end]) {
		return false
	}
	return true
}

// rewriteImportBlock parses specs from just after the block's '(' up
// to and including its matching ')', returning the rewritten block
// body (without parens), any extra declarations resource-import
// loaders produced (to be appended after the block), and the position
// just past ')'.
func rewriteImportBlock(filename, baseDir string, runes []rune, pos int, opts Options) (block string, decls string, newPos int, err error) {
	var blockOut, declsOut strings.Builder
	extraGoImports := map[string]bool{}
	existingPaths := map[string]bool{}
	n := len(runes)
	i := pos
	for {
		if end, ok := skipComment(runes, i); ok {
			blockOut.WriteString(string(runes[i:end]))
			i = end
			continue
		}
		j := i
		for j < n && isSpace(runes[j]) {
			j++
		}
		blockOut.WriteString(string(runes[i:j]))
		i = j
		if i >= n {
			return "", "", 0, fmt.Errorf("%s: unterminated import block", filename)
		}
		if runes[i] == ')' {
			i++
			break
		}

		specStart := i
		var alias string
		switch {
		case runes[i] == '.':
			alias = "."
			i++
		case isIdentPart(runes[i]):
			k := i
			for k < n && isIdentPart(runes[k]) {
				k++
			}
			alias = string(runes[i:k])
			i = k
		}

		// Optional resource-import-only type annotation, reusing Go's
		// type-assertion syntax as a new meaning that's never valid in a
		// real import spec: `alias.(TypeName) "path.json"` tells the
		// loader to decode into TypeName instead of the untyped
		// default. TypeName must already be declared by the caller (in
		// this package or an import) -- gox does not generate it or
		// infer its shape from the file's data.
		var typeName string
		if alias != "" && alias != "." && i+1 < n && runes[i] == '.' && runes[i+1] == '(' {
			i += 2
			tStart := i
			for i < n && (isIdentPart(runes[i]) || runes[i] == '.') {
				i++
			}
			typeName = string(runes[tStart:i])
			if typeName == "" || i >= n || runes[i] != ')' {
				return "", "", 0, fmt.Errorf("%s: expected a type name closed by ')' in %s.(...)", filename, alias)
			}
			i++ // consume ')'
		}

		for i < n && isSpace(runes[i]) {
			i++
		}
		if i >= n || runes[i] != '"' {
			return "", "", 0, fmt.Errorf("%s: expected quoted import path in import block", filename)
		}
		strEnd, _ := skipStringLiteral(runes, i)
		path := string(runes[i+1 : strEnd-1])
		i = strEnd

		if isResourceImport(path) {
			ext := strings.ToLower(filepath.Ext(path))

			loader, ok := loaders[ext]
			if !ok {
				// Recognized only via experimentalExtensions: gox knows
				// the extension but its loader lives in
				// internal/transpile/experimental and isn't wired in
				// unless the caller opted in.
				if !opts.Experimental {
					return "", "", 0, fmt.Errorf("%s: %s imports are experimental; enable with `goxc -experimental` (or transpile.Options{Experimental: true}) -- see internal/transpile/experimental", filename, ext)
				}
				loader, ok = opts.ExperimentalLoaders[ext]
				if !ok {
					return "", "", 0, fmt.Errorf("%s: experimental mode is enabled but no loader is registered for %s imports", filename, ext)
				}
			}

			if typeName != "" && !typeAnnotationAware[ext] {
				return "", "", 0, fmt.Errorf("%s: %s.(%s) %q: %s imports don't support a type annotation", filename, alias, typeName, path, ext)
			}

			ident := alias
			if ident == "" {
				ident = DeriveIdent(path)
			}
			res, lerr := loader(LoaderInput{Alias: ident, AliasGiven: alias != "", TypeName: typeName, Path: path, BaseDir: baseDir, SourceFile: filename})
			if lerr != nil {
				return "", "", 0, lerr
			}
			if res.ImportPath != "" {
				fmt.Fprintf(&blockOut, "%s %q\n", ident, res.ImportPath)
			}
			declsOut.WriteString(res.Decls)
			for _, gi := range res.GoImports {
				extraGoImports[gi] = true
			}
		} else {
			// No trailing "\n" here: the next loop iteration's leading
			// whitespace copy already carries forward this spec's real
			// trailing newline/blank-line from the original source.
			existingPaths[path] = true
			blockOut.WriteString(string(runes[specStart:i]))
		}
	}
	for gi := range extraGoImports {
		if existingPaths[gi] {
			continue
		}
		fmt.Fprintf(&blockOut, "%q\n", gi)
	}
	return blockOut.String(), declsOut.String(), i, nil
}
