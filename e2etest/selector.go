//go:build !(js && wasm)

package e2etest

import (
	"embed"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

//go:embed selector_loader.js
var selectorLoader string

//go:embed selector/main.go selector/native.go selector/pure.go
var selectorSources embed.FS

var (
	selectorOnce   sync.Once
	selectorSource string
	selectorErr    error
)

func selectorScript() (string, error) {
	selectorOnce.Do(func() { selectorSource, selectorErr = buildSelectorScript() })
	return selectorSource, selectorErr
}

func buildSelectorScript() (string, error) {
	tmp, err := os.MkdirTemp("", "e2e-selector-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := writeSelectorModule(tmp); err != nil {
		return "", err
	}
	out := filepath.Join(tmp, "selector.wasm")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, ".")
	build.Dir = tmp
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=", "GO111MODULE=on")
	if msg, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the selector helper: %v\n%s", err, msg)
	}
	wasm, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", err
	}
	execJS, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return "", err
	}
	script := strings.Replace(selectorLoader, "/*WASM_EXEC*/", string(execJS), 1)
	return strings.Replace(script, "/*WASM_B64*/", base64.StdEncoding.EncodeToString(wasm), 1), nil
}

func writeSelectorModule(dir string) error {
	gomod := "module selector\n"
	if v := strings.TrimPrefix(runtime.Version(), "go"); versionPattern.MatchString(v) {
		gomod += "\ngo " + v + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		return err
	}
	for _, name := range []string{"main.go", "native.go", "pure.go"} {
		src, err := selectorSources.ReadFile("selector/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			return err
		}
	}
	return nil
}

var versionPattern = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)
