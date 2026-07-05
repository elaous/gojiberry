package experimental

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/transpile"
)

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

func experimentalOpts() transpile.Options {
	return transpile.Options{Experimental: true, ExperimentalLoaders: Loaders()}
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
	out, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
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
	if _, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts()); err != nil {
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
	_, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
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
	_, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
	if err == nil {
		t.Fatal("expected an error since `capnp` is not installed in this environment")
	}
	if !strings.Contains(err.Error(), "capnp") {
		t.Errorf("expected an actionable install-capnp error, got: %v", err)
	}
}

func TestSQLImportErrorsWithoutSqlcInstalled(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "queries.sql", "-- name: GetX :one\nSELECT 1;\n")
	src := "package main\n\nimport (\n\tq \"queries.sql\"\n)\n\nfunc main() { _ = q }\n"
	_, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
	if err == nil {
		t.Fatal("expected an error since `sqlc` is not installed in this environment")
	}
	if !strings.Contains(err.Error(), "sqlc") {
		t.Errorf("expected an actionable install-sqlc error, got: %v", err)
	}
}

func TestSQLImportErrorsWithoutSqlcConfig(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "queries.sql", "-- name: GetX :one\nSELECT 1;\n")
	installFakeSqlc(t, "") // present on PATH, but no sqlc.yaml anywhere above dir
	src := "package main\n\nimport (\n\tq \"queries.sql\"\n)\n\nfunc main() { _ = q }\n"
	_, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
	if err == nil {
		t.Fatal("expected an error when no sqlc.yaml is found")
	}
	if !strings.Contains(err.Error(), "sqlc.yaml") {
		t.Errorf("expected an actionable missing-config error, got: %v", err)
	}
}

// TestSQLImportInvokesSqlcAndImportsResult stubs `sqlc` on PATH rather
// than requiring the real tool: this exercises gox's own logic
// (finding sqlc.yaml, invoking `sqlc generate -f <config>`, deriving
// the Go import path from gen.go.package/out) without depending on
// sqlc's actual query-analysis/codegen correctness, which isn't gox's
// to test. Since the test itself writes sqlc.yaml, it already knows
// the output directory `sqlc generate` is supposed to populate, so the
// fake script can just write a plausible generated file there
// directly instead of needing gox to report anything back.
func TestSQLImportInvokesSqlcAndImportsResult(t *testing.T) {
	dir := setupModule(t)
	moduleRoot := filepath.Dir(dir)
	writeFile(t, dir, "queries.sql", "-- name: GetX :one\nSELECT 1;\n")
	writeFile(t, moduleRoot, "sqlc.yaml", `version: "2"
sql:
  - engine: "postgresql"
    schema: "schema.sql"
    queries: "pkg/queries.sql"
    gen:
      go:
        package: "db"
        out: "pkg/db"
`)

	outDir := filepath.Join(moduleRoot, "pkg", "db")
	installFakeSqlc(t, "mkdir -p "+shellQuote(outDir)+" && printf 'package db\\n\\nfunc GetX() int { return 1 }\\n' > "+shellQuote(filepath.Join(outDir, "generated.go")))

	src := `package main

import (
	db "queries.sql"
)

func main() {
	if db.GetX() != 1 {
		panic("unexpected")
	}
}
`
	out, err := transpile.TranspileWithOptions(filepath.Join(dir, "main.gox"), []byte(src), experimentalOpts())
	if err != nil {
		t.Fatal(err)
	}
	wantImport := `db "example.com/testmod/pkg/db"`
	if !strings.Contains(string(out), wantImport) {
		t.Errorf("expected generated package import %q, got:\n%s", wantImport, out)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// installFakeSqlc puts a fake `sqlc` executable on PATH for the
// duration of the test, running body (a shell snippet) whenever it's
// invoked as `sqlc generate -f <config>`.
func installFakeSqlc(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sqlc stub is a shell script; skip on windows")
	}
	binDir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "sqlc"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
