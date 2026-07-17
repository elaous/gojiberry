package transpile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// loadJSON handles .json, .jsonc, and .jsonl imports.
//
// Without a type annotation: binds Alias to the decoded value as `any`
// (or `[]any` for .jsonl) and <Alias>Bytes to the raw content, so code
// needing a concrete type can json.Unmarshal(...Bytes, &v) itself.
//
// With `alias.(TypeName) "file.json"`: TypeName must already be
// declared (by you, elsewhere in the package or an import) -- gox
// does not generate it or infer its shape. This just decodes directly
// into that type instead of `any` (or `[]TypeName` for .jsonl), the
// same as the untyped path's json.Unmarshal(...Bytes, &v) but with
// the boilerplate written for you. Because gox isn't guessing a shape
// from sample data, any JSON value (not just a top-level object) is
// fine, and the only failure mode is the ordinary one of your type
// not matching your data -- ordinary encoding/json risk, not a
// gox-specific one.
//
// .jsonc allows // and /* */ comments, stripped before parsing (a
// small string-literal-aware preprocessor, not a jsonc library).
// .jsonl is one JSON value per line; the bound type becomes a slice.
func loadJSON(in LoaderInput) (LoaderResult, error) {
	abs, err := ResolveResourcePath(in)
	if err != nil {
		return LoaderResult{}, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return LoaderResult{}, fmt.Errorf("gox: JSON import %q: %w", in.Path, err)
	}

	ext := strings.ToLower(filepath.Ext(in.Path))
	if ext == ".jsonl" {
		return loadJSONL(in, raw)
	}

	data := raw
	if ext == ".jsonc" {
		data = stripJSONComments(raw)
	}
	// Validate at transpile time so a malformed file is a clear goxc
	// error instead of a runtime panic in generated code.
	if !json.Valid(data) {
		return LoaderResult{}, fmt.Errorf("gox: JSON import %q: invalid JSON", in.Path)
	}

	// <alias>Bytes is always parseable JSON: for .jsonc that's the
	// comment-stripped content, since the original with comments isn't
	// valid input to encoding/json and no one would want to embed the
	// unparseable original instead.
	var decl strings.Builder
	fmt.Fprintf(&decl, "var %sBytes = []byte(%s)\n", in.Alias, strconv.Quote(string(data)))

	goType := "any"
	if in.TypeName != "" {
		goType = in.TypeName
	}
	fmt.Fprintf(&decl, "var %s %s = func() %s {\n\tvar v %s\n\tif err := json.Unmarshal(%sBytes, &v); err != nil {\n\t\tpanic(%s + err.Error())\n\t}\n\treturn v\n}()\n",
		in.Alias, goType, goType, goType, in.Alias, strconv.Quote("gox: invalid embedded JSON "+in.Path+": "))
	return LoaderResult{Decls: decl.String(), GoImports: []string{"encoding/json"}}, nil
}

// loadJSONL handles one-JSON-value-per-line files, binding Alias to a
// slice: []TypeName if annotated (TypeName must already be declared),
// []any otherwise.
func loadJSONL(in LoaderInput, raw []byte) (LoaderResult, error) {
	hasContent := false
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return LoaderResult{}, fmt.Errorf("gox: JSONL import %q: no non-blank lines", in.Path)
	}

	var decl strings.Builder
	fmt.Fprintf(&decl, "var %sBytes = []byte(%s)\n", in.Alias, strconv.Quote(string(raw)))

	elemType := "any"
	if in.TypeName != "" {
		elemType = in.TypeName
	}

	fmt.Fprintf(&decl, "var %s []%s = func() []%s {\n", in.Alias, elemType, elemType)
	fmt.Fprintf(&decl, "\tvar out []%s\n\tfor _, line := range bytes.Split(%sBytes, []byte(\"\\n\")) {\n", elemType, in.Alias)
	decl.WriteString("\t\tif len(bytes.TrimSpace(line)) == 0 {\n\t\t\tcontinue\n\t\t}\n")
	fmt.Fprintf(&decl, "\t\tvar v %s\n\t\tif err := json.Unmarshal(line, &v); err != nil {\n\t\t\tpanic(%s + err.Error())\n\t\t}\n", elemType, strconv.Quote("gox: invalid embedded JSONL line in "+in.Path+": "))
	decl.WriteString("\t\tout = append(out, v)\n\t}\n\treturn out\n}()\n")

	return LoaderResult{Decls: decl.String(), GoImports: []string{"encoding/json", "bytes"}}, nil
}

// stripJSONComments removes // line comments and /* */ block comments
// from JSONC content, leaving string literal contents untouched.
func stripJSONComments(src []byte) []byte {
	var out []byte
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		if c == '"' {
			j := i + 1
			for j < n {
				if src[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if src[j] == '"' {
					j++
					break
				}
				j++
			}
			out = append(out, src[i:j]...)
			i = j
			continue
		}
		if c == '/' && i+1 < n && src[i+1] == '/' {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && (src[i] != '*' || src[i+1] != '/') {
				i++
			}
			i += 2
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}
