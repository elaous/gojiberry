package transpile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// loadJSON handles .json, .jsonc, and .jsonl imports.
//
// Without a type annotation (the pre-existing behavior): binds Alias
// to the decoded value as `any` and <Alias>Bytes to the raw content,
// so code needing a concrete type can json.Unmarshal(...Bytes, &v)
// itself -- no inference happens.
//
// With `alias.(TypeName) "file.json"`: infers a Go struct shape (and
// any nested struct types it needs) from the file's own sample data,
// generates TypeName, and binds Alias to a decoded value of that type
// instead of `any`. This is sample-based inference, not a schema: if
// a field could plausibly hold a different shape of value elsewhere
// (e.g. this sample's `"count": 5` infers int, but production data
// sometimes sends 5.5), hand-declare the type and use the untyped
// path's <Alias>Bytes instead of relying on inference.
//
// .jsonc allows // and /* */ comments, stripped before parsing (a
// small string-literal-aware preprocessor, not a jsonc library).
// .jsonl is one JSON value per line; the bound type becomes a slice
// (inferred/declared type, or []any untyped), decoded line by line.
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
	var sample any
	if err := json.Unmarshal(data, &sample); err != nil {
		return LoaderResult{}, fmt.Errorf("gox: JSON import %q: %w", in.Path, err)
	}

	// <alias>Bytes is always parseable JSON: for .jsonc that's the
	// comment-stripped content, since the original with comments isn't
	// valid input to encoding/json and no one would want to embed the
	// unparseable original instead.
	var decl strings.Builder
	fmt.Fprintf(&decl, "var %sBytes = []byte(%s)\n", in.Alias, strconv.Quote(string(data)))

	if in.TypeName == "" {
		fmt.Fprintf(&decl, "var %s any = func() any {\n\tvar v any\n\tif err := json.Unmarshal(%sBytes, &v); err != nil {\n\t\tpanic(%s + err.Error())\n\t}\n\treturn v\n}()\n",
			in.Alias, in.Alias, strconv.Quote("gox: invalid embedded JSON "+in.Path+": "))
		return LoaderResult{Decls: decl.String(), GoImports: []string{"encoding/json"}}, nil
	}

	obj, ok := sample.(map[string]any)
	if !ok {
		return LoaderResult{}, fmt.Errorf("gox: JSON import %q: %s.(%s) requires a JSON object at the top level, got %T", in.Path, in.Alias, in.TypeName, sample)
	}
	var structs []generatedStruct
	inferJSONType(in.TypeName, obj, &structs)
	for _, s := range structs {
		decl.WriteString(renderGeneratedStruct(s))
	}
	fmt.Fprintf(&decl, "var %s %s = func() %s {\n\tvar v %s\n\tif err := json.Unmarshal(%sBytes, &v); err != nil {\n\t\tpanic(%s + err.Error())\n\t}\n\treturn v\n}()\n",
		in.Alias, in.TypeName, in.TypeName, in.TypeName, in.Alias, strconv.Quote("gox: invalid embedded JSON "+in.Path+": "))
	return LoaderResult{Decls: decl.String(), GoImports: []string{"encoding/json"}}, nil
}

// loadJSONL handles one-JSON-value-per-line files, binding Alias to a
// slice: []TypeName if annotated (inferring TypeName's shape from the
// first line), []any otherwise.
func loadJSONL(in LoaderInput, raw []byte) (LoaderResult, error) {
	var lines [][]byte
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, []byte(l))
	}
	if len(lines) == 0 {
		return LoaderResult{}, fmt.Errorf("gox: JSONL import %q: no non-blank lines", in.Path)
	}

	var decl strings.Builder
	fmt.Fprintf(&decl, "var %sBytes = []byte(%s)\n", in.Alias, strconv.Quote(string(raw)))

	elemType := "any"
	if in.TypeName != "" {
		var sample any
		if err := json.Unmarshal(lines[0], &sample); err != nil {
			return LoaderResult{}, fmt.Errorf("gox: JSONL import %q: first line: %w", in.Path, err)
		}
		obj, ok := sample.(map[string]any)
		if !ok {
			return LoaderResult{}, fmt.Errorf("gox: JSONL import %q: %s.(%s) requires JSON objects, first line was %T", in.Path, in.Alias, in.TypeName, sample)
		}
		var structs []generatedStruct
		inferJSONType(in.TypeName, obj, &structs)
		for _, s := range structs {
			decl.WriteString(renderGeneratedStruct(s))
		}
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
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
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

// generatedStruct and structField describe a Go struct this loader
// will emit; inferJSONType (in loader_hcl_types.go-adjacent helpers
// below) builds these by walking sample JSON data.
type generatedStruct struct {
	Name   string
	Fields []structField
}

type structField struct {
	JSONName string
	GoName   string
	GoType   string
}

// inferJSONType infers the Go type for v, defining new struct(s) into
// *out as needed (appended in child-before-parent order, though Go
// doesn't actually require declaration order for package-level types).
// name is used both as the emitted type name (for objects) and as the
// prefix for any nested types it needs (name+FieldName).
func inferJSONType(name string, v any, out *[]generatedStruct) string {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make([]structField, 0, len(keys))
		for _, k := range keys {
			goName := CapWords(k)
			fieldTypeName := name + goName
			goType := inferJSONType(fieldTypeName, val[k], out)
			fields = append(fields, structField{JSONName: k, GoName: goName, GoType: goType})
		}
		*out = append(*out, generatedStruct{Name: name, Fields: fields})
		return name
	case []any:
		if len(val) == 0 {
			return "[]any"
		}
		return "[]" + inferJSONType(name+"Item", val[0], out)
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		if val == float64(int64(val)) {
			return "int"
		}
		return "float64"
	case nil:
		return "any"
	default:
		return "any"
	}
}

func renderGeneratedStruct(s generatedStruct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type %s struct {\n", s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&b, "\t%s %s `json:%s`\n", f.GoName, f.GoType, strconv.Quote(f.JSONName))
	}
	b.WriteString("}\n")
	return b.String()
}
