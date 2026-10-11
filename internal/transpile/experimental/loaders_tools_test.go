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

// fakeTools puts executables named after the keys of tools on PATH (and only those, ahead of the real
// ones), each a shell script running the body. The loaders shell out to protoc, capnp and friends; this tests
// what xango does around them (the arguments it builds, the import path it reports, the errors it explains)
// without the real compilers.
func fakeTools(t *testing.T, tools map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are shell scripts")
	}
	binDir := t.TempDir()
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func loaderInput(dir, path, alias string) transpile.LoaderInput {
	return transpile.LoaderInput{Alias: alias, Path: path, BaseDir: dir, SourceFile: filepath.Join(dir, "main.xg")}
}

func TestLoadProtoNeedsTheGoPlugin(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "books.proto", `syntax = "proto3";`)
	fakeTools(t, map[string]string{"protoc": "exit 0"})
	t.Setenv("PATH", filepath.Dir(mustLookPath(t, "protoc"))) // protoc, and no protoc-gen-go
	_, err := LoadProto(loaderInput(dir, "books.proto", "books"))
	if err == nil || !strings.Contains(err.Error(), "protoc-gen-go") {
		t.Fatalf("an actionable error naming the plugin: %v", err)
	}
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadProtoRunsProtocWithTheModuleMapping(t *testing.T) {
	dir := setupModule(t)
	moduleRoot := filepath.Dir(dir)
	writeFile(t, dir, "books.proto", `syntax = "proto3";`)
	argsFile := filepath.Join(t.TempDir(), "args")
	fakeTools(t, map[string]string{
		"protoc":        `printf '%s\n' "$@" > ` + shellQuote(argsFile),
		"protoc-gen-go": "exit 0",
	})
	res, err := LoadProto(loaderInput(dir, "books.proto", "books"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ImportPath != "example.com/testmod/internal/xggen/proto/books" {
		t.Errorf("import path %q", res.ImportPath)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--proto_path=" + dir, "--go_out=" + moduleRoot, "--go_opt=module=example.com/testmod", "--go_opt=Mbooks.proto=example.com/testmod/internal/xggen/proto/books", filepath.Join(dir, "books.proto")} {
		if !strings.Contains(string(args), want) {
			t.Errorf("protoc was not given %q:\n%s", want, args)
		}
	}
	if strings.Contains(string(args), "go-grpc") {
		t.Error("no gRPC plugin on PATH: none asked for")
	}
	// with the gRPC plugin on PATH it is asked for too
	fakeTools(t, map[string]string{"protoc": `printf '%s\n' "$@" > ` + shellQuote(argsFile), "protoc-gen-go": "exit 0", "protoc-gen-go-grpc": "exit 0"})
	if _, err := LoadProto(loaderInput(dir, "books.proto", "books")); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--go-grpc_out="+moduleRoot) {
		t.Errorf("the gRPC plugin is used when present:\n%s", args)
	}
}

func TestLoadProtoReportsProtocFailuresAndAliasClashes(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "books.proto", `syntax = "proto3";`)
	writeFile(t, dir, "other.proto", `syntax = "proto3";`)
	fakeTools(t, map[string]string{"protoc": `echo "books.proto:1:1: syntax error" >&2; exit 1`, "protoc-gen-go": "exit 0"})
	if _, err := LoadProto(loaderInput(dir, "books.proto", "books")); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("protoc's own message is shown: %v", err)
	}
	if _, err := LoadProto(loaderInput(dir, "nope.proto", "x")); err == nil {
		t.Error("a missing schema file is an error")
	}
	fakeTools(t, map[string]string{"protoc": "exit 0", "protoc-gen-go": "exit 0"})
	if _, err := LoadProto(loaderInput(dir, "books.proto", "same")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProto(loaderInput(dir, "other.proto", "same")); err == nil || !strings.Contains(err.Error(), "another alias") {
		t.Errorf("one alias, two schemas: %v", err)
	}
	outside := t.TempDir() // not in a Go module
	writeFile(t, outside, "x.proto", `syntax = "proto3";`)
	if _, err := LoadProto(loaderInput(outside, "x.proto", "x")); err == nil {
		t.Error("outside a module there is no import path to give")
	}
}

func TestLoadCapnProto(t *testing.T) {
	dir := setupModule(t)
	schema := writeFile(t, dir, "books.capnp", "@0xdbb9ad1f14bf0b36;\nusing Go = import \"/go.capnp\";\n$Go.package(\"books\");\n$Go.import(\"example.com/testmod/books\");\n")
	argsFile := filepath.Join(t.TempDir(), "args")
	good := `printf '%s\n' "$@" > ` + shellQuote(argsFile) + `; : > ` + shellQuote(schema+".go")

	// the compiler missing, or its Go plugin
	fakeTools(t, map[string]string{"capnp": "exit 0"})
	t.Setenv("PATH", filepath.Dir(mustLookPath(t, "capnp")))
	if _, err := LoadCapnProto(loaderInput(dir, "books.capnp", "books")); err == nil || !strings.Contains(err.Error(), "capnpc-go") {
		t.Fatalf("an error naming the plugin: %v", err)
	}

	// the schema must say where its Go code goes
	noAnnotation := writeFile(t, dir, "plain.capnp", "@0xdbb9ad1f14bf0b37;\n")
	_ = noAnnotation
	fakeTools(t, map[string]string{"capnp": good, "capnpc-go": "exit 0"})
	if _, err := LoadCapnProto(loaderInput(dir, "plain.capnp", "plain")); err == nil || !strings.Contains(err.Error(), "$Go.import") {
		t.Fatalf("no annotation: %v", err)
	}

	// the happy path reports the import path the schema declared, and passes the include directories on
	t.Setenv("XANGO_CAPNP_INCLUDE", "/usr/share/capnp,/opt/capnp")
	res, err := LoadCapnProto(loaderInput(dir, "books.capnp", "books"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ImportPath != "example.com/testmod/books" {
		t.Errorf("import path %q", res.ImportPath)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"compile", "-I" + dir, "-I/usr/share/capnp", "-I/opt/capnp", "-ogo", schema} {
		if !strings.Contains(string(args), want) {
			t.Errorf("capnp was not given %q:\n%s", want, args)
		}
	}

	// a compiler that fails, and one that succeeds without writing the Go file
	fakeTools(t, map[string]string{"capnp": `echo "books.capnp:2:1: bad" >&2; exit 1`, "capnpc-go": "exit 0"})
	if _, err := LoadCapnProto(loaderInput(dir, "books.capnp", "books")); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("capnp's message is shown: %v", err)
	}
	_ = os.Remove(schema + ".go")
	fakeTools(t, map[string]string{"capnp": "exit 0", "capnpc-go": "exit 0"})
	if _, err := LoadCapnProto(loaderInput(dir, "books.capnp", "books")); err == nil || !strings.Contains(err.Error(), "did not produce") {
		t.Errorf("no output file: %v", err)
	}
}
