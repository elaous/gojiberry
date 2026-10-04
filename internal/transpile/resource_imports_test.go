package transpile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupModule creates a temp module <tmp>/go.mod + <tmp>/pkg/ and
// returns the pkg directory, for tests that need a real module root
// (HCL/proto/capnp loaders generate a subpackage relative to one).
func setupModule(t *testing.T) (pkgDir string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/testmod\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir = filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return pkgDir
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mustCompileAndRun writes src as filename inside dir (a real module's
// package directory) and confirms `go run` actually compiles and
// executes it -- not just that it looks plausible.
func mustCompileAndRun(t *testing.T, dir, filename, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed to compile/run: %v\n%s", err, out)
	}
}

func TestOrdinaryImportsPassThroughUnchanged(t *testing.T) {
	dir := setupModule(t)
	src := "package main\n\nimport (\n\t\"fmt\"\n\tfoo \"example.com/other/foo\"\n)\n\nfunc main() { fmt.Println(foo.X) }\n"
	got, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"fmt"`) || !strings.Contains(string(got), `foo "example.com/other/foo"`) {
		t.Errorf("ordinary imports should pass through unchanged, got:\n%s", got)
	}
}

func TestJSONImportGeneratesDeclsAndCompiles(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{"greeting": "hi"}`)
	src := `package main

import (
	config "config.json"
)

func main() {
	_ = config
	_ = configBytes
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"encoding/json"`) {
		t.Errorf("expected encoding/json to be added to imports, got:\n%s", got)
	}
	if !strings.Contains(got, "var configBytes = []byte(") {
		t.Errorf("expected embedded bytes var, got:\n%s", got)
	}
	if !strings.Contains(got, "var config any = func() any {") {
		t.Errorf("expected decoded any var, got:\n%s", got)
	}
	if strings.Contains(got, `config "config.json"`) {
		t.Errorf("resource import spec should not remain as a Go import line, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestJSONImportDoesNotDuplicateManualEncodingJSONImport(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{"greeting": "hi"}`)
	src := `package main

import (
	"encoding/json"

	config "config.json"
)

func main() {
	var v map[string]string
	_ = json.Unmarshal(configBytes, &v)
	_ = config
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, `"encoding/json"`) != 1 {
		t.Errorf("expected exactly one encoding/json import, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

// TestTypedJSONImportDecodesIntoUserDeclaredType covers the current
// design: TypeName in `alias.(TypeName)` must already be declared by
// the caller (xango does not generate it or infer its shape), so any
// JSON shape works, not just a top-level object -- including this
// case, where nested/array/scalar fields all just flow straight into
// a struct the test itself writes.
func TestTypedJSONImportDecodesIntoUserDeclaredType(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{
		"title": "gojiberry",
		"count": 3,
		"tags": ["a", "b"],
		"address": {"city": "Springfield"}
	}`)
	src := `package main

import (
	config.(ConfigType) "config.json"
)

type Address struct {
	City string ` + "`json:\"city\"`" + `
}

type ConfigType struct {
	Title   string  ` + "`json:\"title\"`" + `
	Count   int     ` + "`json:\"count\"`" + `
	Tags    []string ` + "`json:\"tags\"`" + `
	Address Address  ` + "`json:\"address\"`" + `
}

func main() {
	var _ ConfigType = config
	if config.Title != "gojiberry" || config.Count != 3 || config.Address.City != "Springfield" {
		panic("unexpected config contents")
	}
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "type ConfigType struct") == false {
		t.Fatalf("expected the user's own ConfigType declaration to survive untouched, got:\n%s", got)
	}
	if strings.Count(got, "type ConfigType struct") != 1 {
		t.Errorf("expected xango not to generate a second ConfigType, got:\n%s", got)
	}
	if !strings.Contains(got, "var config ConfigType") {
		t.Errorf("expected config bound as ConfigType, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestTypedJSONImportAcceptsNonObjectRootNow(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "list.json", `[1, 2, 3]`)
	src := "package main\n\nimport (\n\tv.(Ints) \"list.json\"\n)\n\nfunc main() {\n\tvar _ Ints = v\n\tif len(v) != 3 {\n\t\tpanic(\"unexpected\")\n\t}\n}\n\ntype Ints []int\n"
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestJSONCImportTypedDecodesIntoUserDeclaredType(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.jsonc", `{
		// this is a comment
		"title": "gojiberry", /* inline */ "count": 2
	}`)
	src := "package main\n\nimport (\n\tconfig.(ConfigType) \"config.jsonc\"\n)\n\ntype ConfigType struct {\n\tTitle string `json:\"title\"`\n\tCount int `json:\"count\"`\n}\n\nfunc main() {\n\t_ = config.Title\n\t_ = config.Count\n}\n"
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestJSONCImportUntypedDecodesStrippedContent(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.jsonc", "{\n  // a comment\n  \"title\": \"gojiberry\"\n}\n")
	src := `package main

import (
	"encoding/json"

	config "config.jsonc"
)

func main() {
	var m map[string]any
	if err := json.Unmarshal(configBytes, &m); err != nil {
		panic(err)
	}
	if m["title"] != "gojiberry" {
		panic("unexpected: " + m["title"].(string))
	}
	_ = config
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestJSONLImportUntyped(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "events.jsonl", "{\"name\":\"a\"}\n{\"name\":\"b\"}\n\n{\"name\":\"c\"}\n")
	src := `package main

import (
	events "events.jsonl"
)

func main() {
	if len(events) != 3 {
		panic("expected 3 events")
	}
	first, ok := events[0].(map[string]any)
	if !ok || first["name"] != "a" {
		panic("unexpected first event")
	}
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "var events []any") {
		t.Errorf("expected events bound as []any, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestJSONLImportTypedDecodesIntoUserDeclaredType(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "events.jsonl", "{\"name\":\"a\"}\n{\"name\":\"b\"}\n\n{\"name\":\"c\"}\n")
	src := `package main

import (
	events.(Event) "events.jsonl"
)

type Event struct {
	Name string ` + "`json:\"name\"`" + `
}

func main() {
	if len(events) != 3 {
		panic("expected 3 events")
	}
	if events[0].Name != "a" || events[2].Name != "c" {
		panic("unexpected event contents")
	}
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "var events []Event") {
		t.Errorf("expected events bound as []Event, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

// TestExperimentalExtensionsRejectedWithoutOptIn covers the default
// (non-experimental) gating boundary for .hcl/.proto/.capnp/.sql --
// their actual loaders now live in internal/transpile/experimental
// and are tested there (with Options{Experimental: true}); this only
// confirms xango refuses to run them silently.
func TestExperimentalExtensionsRejectedWithoutOptIn(t *testing.T) {
	for _, tc := range []struct {
		ext     string
		content string
	}{
		{".hcl", `table "x" { column "id" { type = "string" } }`},
		{".proto", `syntax = "proto3";`},
		{".capnp", "@0xdbb9ad1f14bf0b36;\n"},
		{".sql", "SELECT 1;\n"},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			dir := setupModule(t)
			writeFile(t, dir, "resource"+tc.ext, tc.content)
			src := "package main\n\nimport (\n\tr \"resource" + tc.ext + "\"\n)\n\nfunc main() { _ = r }\n"
			_, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
			if err == nil {
				t.Fatalf("expected %s imports to be rejected without Options.Experimental", tc.ext)
			}
			if !strings.Contains(err.Error(), "experimental") {
				t.Errorf("expected an actionable experimental-mode error, got: %v", err)
			}
		})
	}
}

// TestTypedJSONAnnotationWorksByDefault confirms the .(TypeName)
// annotation needs no experimental opt-in: it isn't gated, since it no
// longer infers/generates anything (see TestTypedJSONImport
// DecodesIntoUserDeclaredType) -- the only remaining risk is the
// ordinary one of a hand-written type not matching the data, identical
// to writing json.Unmarshal by hand.
func TestTypedJSONAnnotationWorksByDefault(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{"title": "gojiberry"}`)
	src := "package main\n\nimport (\n\tconfig.(ConfigType) \"config.json\"\n)\n\ntype ConfigType struct {\n\tTitle string `json:\"title\"`\n}\n\nfunc main() {\n\tif config.Title != \"gojiberry\" {\n\t\tpanic(\"unexpected\")\n\t}\n}\n"
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

// TestTypedJSONAnnotationWithUndeclaredTypeFailsAtGoCompile confirms
// xango doesn't try to verify TypeName actually exists -- that's the Go
// compiler's job, not a source-to-source transpiler's; using a type
// xango never heard of should transpile fine and fail where any other
// undefined-identifier mistake would.
func TestTypedJSONAnnotationWithUndeclaredTypeFailsAtGoCompile(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{"title": "gojiberry"}`)
	src := "package main\n\nimport (\n\tconfig.(NeverDeclared) \"config.json\"\n)\n\nfunc main() { _ = config }\n"
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", ".")
	cmd.Dir = dir
	buildOut, buildErr := cmd.CombinedOutput()
	if buildErr == nil {
		t.Fatal("expected a Go compile error for an undeclared type name")
	}
	if !strings.Contains(string(buildOut), "NeverDeclared") {
		t.Errorf("expected the compiler error to name the undeclared type, got:\n%s", buildOut)
	}
}

func TestMissingResourceFileErrorsWithResolvedPath(t *testing.T) {
	dir := setupModule(t)
	src := "package main\n\nimport (\n\tconfig \"missing.json\"\n)\n\nfunc main() { _ = config }\n"
	_, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err == nil {
		t.Fatal("expected an error for a missing resource file")
	}
	if !strings.Contains(err.Error(), "missing.json") {
		t.Errorf("expected error to name the missing file, got: %v", err)
	}
}
