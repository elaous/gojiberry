package transpile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var capnpGoImportRe = regexp.MustCompile(`\$Go\.import\(\s*"([^"]+)"\s*\)`)

// loadCapnProto generates Go bindings for a .capnp file by shelling
// out to the real capnp/capnpc-go toolchain (gox does not reimplement
// a Cap'n Proto compiler).
//
// Unlike protoc-gen-go, capnpc-go has no external flag to say where
// generated code should live or what Go import path to give it -- it
// takes both solely from a `$Go.import("...")`/`$Go.package("...")`
// annotation the schema itself must declare (the real, documented
// go-capnp convention: https://pkg.go.dev/capnproto.org/go/capnp/v3),
// and writes the output next to the source file. So unlike HCL/proto,
// gox can't choose the destination here -- it runs the compiler and
// reports back whatever import path the schema declared for itself.
func loadCapnProto(in loaderInput) (loaderResult, error) {
	abs, err := resolveResourcePath(in)
	if err != nil {
		return loaderResult{}, err
	}
	if _, err := exec.LookPath("capnp"); err != nil {
		return loaderResult{}, fmt.Errorf("gox: .capnp import %q requires the `capnp` compiler on PATH; install it from https://capnproto.org/install.html", in.Path)
	}
	if _, err := exec.LookPath("capnpc-go"); err != nil {
		return loaderResult{}, fmt.Errorf("gox: .capnp import %q requires the `capnpc-go` plugin on PATH; install it with `go install capnproto.org/go/capnp/v3/capnpc-go@latest`", in.Path)
	}

	src, err := os.ReadFile(abs)
	if err != nil {
		return loaderResult{}, fmt.Errorf("gox: .capnp import %q: %w", in.Path, err)
	}
	m := capnpGoImportRe.FindSubmatch(src)
	if m == nil {
		return loaderResult{}, fmt.Errorf(
			"gox: .capnp import %q has no `$Go.import(\"...\")` annotation; add\n"+
				"  using Go = import \"/go.capnp\";\n  $Go.package(\"yourpkg\");\n  $Go.import(\"your/module/path\");\n"+
				"per https://pkg.go.dev/capnproto.org/go/capnp/v3 so capnpc-go knows where the generated code belongs", in.Path)
	}
	importPath := string(m[1])

	includeDirs := []string{filepath.Dir(abs)}
	if extra := os.Getenv("GOX_CAPNP_INCLUDE"); extra != "" {
		includeDirs = append(includeDirs, strings.Split(extra, ",")...)
	}
	args := make([]string, 0, len(includeDirs)+2)
	for _, d := range includeDirs {
		args = append(args, "-I"+d)
	}
	args = append(args, "-ogo", abs)

	cmd := exec.Command("capnp", append([]string{"compile"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return loaderResult{}, fmt.Errorf("gox: capnp compile failed for %q: %w\n%s", in.Path, err, out)
	}

	generated := abs + ".go"
	if _, err := os.Stat(generated); err != nil {
		return loaderResult{}, fmt.Errorf("gox: capnp compile for %q did not produce %s: %w", in.Path, generated, err)
	}

	return loaderResult{ImportPath: importPath}, nil
}
