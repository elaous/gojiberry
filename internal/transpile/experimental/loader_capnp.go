package experimental

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elaous/gojiberry/internal/transpile"
)

var capnpGoImportRe = regexp.MustCompile(`\$Go\.import\(\s*"([^"]+)"\s*\)`)

func LoadCapnProto(in transpile.LoaderInput) (transpile.LoaderResult, error) {
	abs, err := transpile.ResolveResourcePath(in)
	if err != nil {
		return transpile.LoaderResult{}, err
	}
	if _, err := exec.LookPath("capnp"); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .capnp import %q requires the `capnp` compiler on PATH; install it from https://capnproto.org/install.html", in.Path)
	}
	if _, err := exec.LookPath("capnpc-go"); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .capnp import %q requires the `capnpc-go` plugin on PATH; install it with `go install capnproto.org/go/capnp/v3/capnpc-go@latest`", in.Path)
	}

	src, err := os.ReadFile(abs)
	if err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .capnp import %q: %w", in.Path, err)
	}
	m := capnpGoImportRe.FindSubmatch(src)
	if m == nil {
		return transpile.LoaderResult{}, fmt.Errorf(
			"xango: .capnp import %q has no `$Go.import(\"...\")` annotation; add\n"+
				"  using Go = import \"/go.capnp\";\n  $Go.package(\"yourpkg\");\n  $Go.import(\"your/module/path\");\n"+
				"per https://pkg.go.dev/capnproto.org/go/capnp/v3 so capnpc-go knows where the generated code belongs", in.Path)
	}
	importPath := string(m[1])

	includeDirs := []string{filepath.Dir(abs)}
	if extra := os.Getenv("XANGO_CAPNP_INCLUDE"); extra != "" {
		includeDirs = append(includeDirs, strings.Split(extra, ",")...)
	}
	args := make([]string, 0, len(includeDirs)+2)
	for _, d := range includeDirs {
		args = append(args, "-I"+d)
	}
	args = append(args, "-ogo", abs)

	cmd := exec.Command("capnp", append([]string{"compile"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: capnp compile failed for %q: %w\n%s", in.Path, err, out)
	}

	generated := abs + ".go"
	if _, err := os.Stat(generated); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: capnp compile for %q did not produce %s: %w", in.Path, generated, err)
	}

	return transpile.LoaderResult{ImportPath: importPath}, nil
}
