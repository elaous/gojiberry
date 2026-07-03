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

func TestSQLImportGeneratesNamedQueries(t *testing.T) {
	dir := setupModule(t)
	moduleRoot := filepath.Dir(dir)
	writeFile(t, dir, "bob.sql", `-- @var: SelectFromBlob
SELECT * FROM blob;

-- @var: CountBlobs
SELECT COUNT(*) FROM blob;
`)
	src := "package main\n\nimport (\n\tqueries \"bob.sql\"\n)\n\nfunc main() { _ = queries.SelectFromBlob; _ = queries.CountBlobs }\n"
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	wantImport := `queries "example.com/testmod/internal/goxgen/sql/queries"`
	if !strings.Contains(string(out), wantImport) {
		t.Errorf("expected generated package import %q, got:\n%s", wantImport, out)
	}

	genPath := filepath.Join(moduleRoot, "internal", "goxgen", "sql", "queries", "queries.go")
	genSrc, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("expected generated queries file at %s: %v", genPath, err)
	}
	normalized := strings.Join(strings.Fields(string(genSrc)), " ")
	if !strings.Contains(normalized, "package queries") {
		t.Errorf("expected package queries, got:\n%s", genSrc)
	}
	if !strings.Contains(normalized, "SelectFromBlob = `SELECT * FROM blob`") {
		t.Errorf("expected SelectFromBlob var with semicolon trimmed, got:\n%s", genSrc)
	}
	if !strings.Contains(normalized, "CountBlobs = `SELECT COUNT(*) FROM blob`") {
		t.Errorf("expected CountBlobs var, got:\n%s", genSrc)
	}
}

func TestSQLImportErrorsWithNoAnnotations(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "plain.sql", "SELECT 1;\n")
	src := "package main\n\nimport (\n\tq \"plain.sql\"\n)\n\nfunc main() { _ = q }\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error for a .sql file with no @var annotations")
	}
	if !strings.Contains(err.Error(), "@var") {
		t.Errorf("expected error to mention @var, got: %v", err)
	}
}

func TestHCLImportGeneratesModelsAndMigration(t *testing.T) {
	dir := setupModule(t)
	moduleRoot := filepath.Dir(dir)
	writeFile(t, dir, "schema.hcl", `
table "users" {
  column "id" {
    type = "uuid"
    primary_key = true
  }
  column "email" {
    type = "string"
    unique = true
  }
  column "created_at" {
    type = "timestamp"
    not_null = true
  }
}
`)
	src := `package main

import (
	schema "schema.hcl"
)

func main() {
	u := schema.Users{}
	_ = u
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	wantImport := `schema "example.com/testmod/internal/goxgen/hcl/schema"`
	if !strings.Contains(got, wantImport) {
		t.Errorf("expected generated package import %q, got:\n%s", wantImport, got)
	}

	modelsPath := filepath.Join(moduleRoot, "internal", "goxgen", "hcl", "schema", "models.go")
	modelsSrc, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatalf("expected generated models file at %s: %v", modelsPath, err)
	}
	// gofmt column-aligns struct fields, so collapse repeated whitespace
	// before matching rather than asserting exact spacing.
	normalized := strings.Join(strings.Fields(string(modelsSrc)), " ")
	for _, want := range []string{"package schema", "type Users struct", "Id string", "Email string", "CreatedAt time.Time", `"time"`} {
		if !strings.Contains(normalized, want) {
			t.Errorf("expected generated models.go to contain %q, got:\n%s", want, modelsSrc)
		}
	}

	migrationPath := filepath.Join(moduleRoot, "internal", "goxgen", "hcl", "schema", "migrations", "0001_init.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("expected generated migration at %s: %v", migrationPath, err)
	}
	for _, want := range []string{"CREATE TABLE IF NOT EXISTS users", "id UUID PRIMARY KEY", "email TEXT UNIQUE", "created_at TIMESTAMP NOT NULL"} {
		if !strings.Contains(string(migrationSQL), want) {
			t.Errorf("expected generated migration to contain %q, got:\n%s", want, migrationSQL)
		}
	}
}

func TestHCLImportWithReferences(t *testing.T) {
	dir := setupModule(t)
	moduleRoot := filepath.Dir(dir)
	writeFile(t, dir, "schema.hcl", `
table "users" {
  column "id" {
    type = "uuid"
    primary_key = true
  }
}
table "books" {
  column "id" {
    type = "uuid"
    primary_key = true
  }
  column "author_id" {
    type = "uuid"
    references = "users.id"
  }
}
`)
	src := "package main\n\nimport (\n\tschema \"schema.hcl\"\n)\n\nfunc main() { _ = schema.Books{} }\n"
	if _, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src)); err != nil {
		t.Fatal(err)
	}
	migrationSQL, err := os.ReadFile(filepath.Join(moduleRoot, "internal", "goxgen", "hcl", "schema", "migrations", "0001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrationSQL), "author_id UUID REFERENCES users(id)") {
		t.Errorf("expected a REFERENCES clause derived from the references attribute, got:\n%s", migrationSQL)
	}
}

func TestProtoImportErrorsWithoutProtocInstalled(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "books.proto", `syntax = "proto3";`)
	src := "package main\n\nimport (\n\tbooks \"books.proto\"\n)\n\nfunc main() {}\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error since `protoc` is not installed in this environment")
	}
	if !strings.Contains(err.Error(), "protoc") {
		t.Errorf("expected an actionable install-protoc error, got: %v", err)
	}
}

func TestCapnpImportErrorsWithoutCapnpInstalled(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "books.capnp", "@0xdbb9ad1f14bf0b36;\n")
	src := "package main\n\nimport (\n\tbooks \"books.capnp\"\n)\n\nfunc main() {}\n"
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error since `capnp` is not installed in this environment")
	}
	if !strings.Contains(err.Error(), "capnp") {
		t.Errorf("expected an actionable install-capnp error, got: %v", err)
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
