//go:build !(js && wasm)

package e2etest

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

//go:embed selector_loader.js
var selectorLoader string

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
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("e2etest: cannot find the source of the selector helper")
	}
	dir, err := selectorDir(file)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "e2e-selector-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	out := filepath.Join(tmp, "selector.wasm")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./selector")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0")
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

func selectorDir(file string) (string, error) {
	if dir := filepath.Dir(file); filepath.IsAbs(dir) {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, nil
		}
	}
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", "github.com/elaous/gojiberry/internal/e2etest").Output()
	if err != nil {
		return "", fmt.Errorf("e2etest: cannot find the source of the selector helper: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}
