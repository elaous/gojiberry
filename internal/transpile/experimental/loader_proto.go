package experimental

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/elaous/gojiberry/internal/transpile"
)

// loadProto generates Go bindings for a .proto file by shelling out to
// the real protoc/protoc-gen-go toolchain (gox does not reimplement a
// protobuf compiler) and rewrites the import to point at the result.
//
// protoc-gen-go requires knowing the target Go import path for a
// .proto file; rather than requiring the .proto to declare its own
// `option go_package`, gox tells protoc directly via the `-M` file
// mapping flag (a real, documented protoc-gen-go option) that this
// file's output belongs at <module>/internal/goxgen/proto/<alias>, so
// gox -- not the .proto author -- controls where generated code lands.
func LoadProto(in transpile.LoaderInput) (transpile.LoaderResult, error) {
	abs, err := transpile.ResolveResourcePath(in)
	if err != nil {
		return transpile.LoaderResult{}, err
	}
	if _, err := exec.LookPath("protoc"); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: .proto import %q requires `protoc` on PATH; install it from https://grpc.io/docs/protoc-installation/", in.Path)
	}
	if _, err := exec.LookPath("protoc-gen-go"); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: .proto import %q requires `protoc-gen-go` on PATH; install it with `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`", in.Path)
	}

	moduleRoot, modulePath, err := transpile.FindModuleRoot(in.BaseDir)
	if err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: .proto import %q: %w", in.Path, err)
	}
	importPath := modulePath + "/internal/goxgen/proto/" + in.Alias
	protoDir := filepath.Join(moduleRoot, "internal", "goxgen", "proto", in.Alias)
	if err := os.MkdirAll(protoDir, 0o755); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: .proto import %q: %w", in.Path, err)
	}
	if err := claimAlias(protoDir, abs); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: .proto import %q: %w", in.Path, err)
	}

	base := filepath.Base(abs)
	args := []string{
		"--proto_path=" + filepath.Dir(abs),
		"--go_out=" + moduleRoot,
		"--go_opt=module=" + modulePath,
		"--go_opt=M" + base + "=" + importPath,
	}
	if _, err := exec.LookPath("protoc-gen-go-grpc"); err == nil {
		args = append(args,
			"--go-grpc_out="+moduleRoot,
			"--go-grpc_opt=module="+modulePath,
			"--go-grpc_opt=M"+base+"="+importPath,
		)
	}
	args = append(args, abs)

	cmd := exec.Command("protoc", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("gox: protoc failed for %q: %w\n%s", in.Path, err, out)
	}

	return transpile.LoaderResult{ImportPath: importPath}, nil
}
