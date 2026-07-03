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
	got, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
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
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
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
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, `"encoding/json"`) != 1 {
		t.Errorf("expected exactly one encoding/json import, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestCSSImportBindsAliasToStringContent(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "App.css", "body { color: red; }")
	src := `package main

import (
	"App.css"
)

func main() {
	_ = App_css
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `var App_css = "body { color: red; }"`) {
		t.Errorf("expected derived identifier App_css bound to file content, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestCSSImportWithExplicitAlias(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "styles.css", "* { margin: 0; }")
	src := `package main

import (
	styles "styles.css"
)

func main() { _ = styles }
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `var styles = "* { margin: 0; }"`) {
		t.Errorf("expected explicit alias to be used over derived name, got:\n%s", out)
	}
}

func TestTypedJSONImportInfersStructAndCompiles(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{
		"title": "gojiberry",
		"count": 3,
		"ratio": 1.5,
		"active": true,
		"tags": ["a", "b"],
		"address": {"city": "Springfield", "zip": "00000"}
	}`)
	src := `package main

import (
	config.(ConfigType) "config.json"
)

func main() {
	var _ ConfigType = config
	_ = config.Title
	_ = config.Count
	_ = config.Ratio
	_ = config.Active
	_ = config.Tags
	_ = config.Address.City
}
`
	out, err := TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), Options{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"type ConfigType struct",
		"type ConfigTypeAddress struct",
		`Title string ` + "`json:\"title\"`",
		`Count int ` + "`json:\"count\"`",
		`Ratio float64 ` + "`json:\"ratio\"`",
		`Active bool ` + "`json:\"active\"`",
		`Tags []string ` + "`json:\"tags\"`",
		"Address ConfigTypeAddress",
		"var config ConfigType",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected generated code to contain %q, got:\n%s", want, got)
		}
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestTypedJSONImportRejectsNonObjectRoot(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "list.json", `[1, 2, 3]`)
	src := "package main\n\nimport (\n\tv.(V) \"list.json\"\n)\n\nfunc main() { _ = v }\n"
	_, err := TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), Options{Experimental: true})
	if err == nil {
		t.Fatal("expected an error for a non-object JSON root with a type annotation")
	}
	if !strings.Contains(err.Error(), "top level") {
		t.Errorf("expected a top-level-object error, got: %v", err)
	}
}

func TestTypeAnnotationRejectedForUnsupportedExtension(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "App.css", "body{}")
	src := "package main\n\nimport (\n\tv.(V) \"App.css\"\n)\n\nfunc main() { _ = v }\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error using a type annotation on a .css import")
	}
	if !strings.Contains(err.Error(), "don't support a type annotation") {
		t.Errorf("expected a clear unsupported-annotation error, got: %v", err)
	}
}

func TestJSONCImportStripsCommentsAndCompiles(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.jsonc", `{
		// this is a comment
		"title": "gojiberry", /* inline */ "count": 2
	}`)
	src := `package main

import (
	config.(ConfigType) "config.jsonc"
)

func main() {
	_ = config.Title
	_ = config.Count
}
`
	out, err := TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), Options{Experimental: true})
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
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestJSONLImportUntypedAndTyped(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "events.jsonl", "{\"name\":\"a\"}\n{\"name\":\"b\"}\n\n{\"name\":\"c\"}\n")
	src := `package main

import (
	events.(Event) "events.jsonl"
)

func main() {
	if len(events) != 3 {
		panic("expected 3 events")
	}
	if events[0].Name != "a" || events[2].Name != "c" {
		panic("unexpected event contents")
	}
}
`
	out, err := TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), Options{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "type Event struct") {
		t.Errorf("expected generated Event struct, got:\n%s", got)
	}
	if !strings.Contains(got, "var events []Event") {
		t.Errorf("expected events bound as []Event, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestSCSSImportErrorsWithoutSassInstalled(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "App.scss", "$c: red; body { color: $c; }")
	src := "package main\n\nimport (\n\t\"App.scss\"\n)\n\nfunc main() {}\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error since `sass` is not installed in this environment")
	}
	if !strings.Contains(err.Error(), "sass") || !strings.Contains(err.Error(), "sass-lang.com") {
		t.Errorf("expected an actionable install-sass error, got: %v", err)
	}
}

// TestExperimentalExtensionsRejectedWithoutOptIn covers the default
// (non-experimental) gating boundary for .hcl/.proto/.capnp/.sql --
// their actual loaders now live in internal/transpile/experimental
// and are tested there (with Options{Experimental: true}); this only
// confirms gox refuses to run them silently.
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
			_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
			if err == nil {
				t.Fatalf("expected %s imports to be rejected without Options.Experimental", tc.ext)
			}
			if !strings.Contains(err.Error(), "experimental") {
				t.Errorf("expected an actionable experimental-mode error, got: %v", err)
			}
		})
	}
}

func TestTypedJSONAnnotationRejectedWithoutOptIn(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "config.json", `{"title": "gojiberry"}`)
	src := "package main\n\nimport (\n\tconfig.(ConfigType) \"config.json\"\n)\n\nfunc main() { _ = config }\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected the .(TypeName) annotation to be rejected without Options.Experimental")
	}
	if !strings.Contains(err.Error(), "experimental") {
		t.Errorf("expected an actionable experimental-mode error, got: %v", err)
	}
}

func TestMissingResourceFileErrorsWithResolvedPath(t *testing.T) {
	dir := setupModule(t)
	src := "package main\n\nimport (\n\tconfig \"missing.json\"\n)\n\nfunc main() { _ = config }\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error for a missing resource file")
	}
	if !strings.Contains(err.Error(), "missing.json") {
		t.Errorf("expected error to name the missing file, got: %v", err)
	}
}
