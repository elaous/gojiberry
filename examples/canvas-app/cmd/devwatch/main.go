package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elaous/gojiberry/devwatch"
)

var devserverBin = filepath.Join(os.TempDir(), "xango-canvas-devwatch-server")

func runStep(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("devwatch: %s %s failed: %v", name, strings.Join(args, " "), err)
		return false
	}
	return true
}

func rebuild() bool {
	if !runStep("make", "generate") {
		return false
	}
	if !runStep("make", "wasm") {
		return false
	}
	return runStep("go", "build", "-o", devserverBin, "./cmd/devserver")
}

func main() {
	defer func() { _ = os.Remove(devserverBin) }() // best-effort removal of the scratch binary; it may not exist

	err := devwatch.Run(devwatch.Config{
		Root: ".",
		Ignore: func(path string) bool {
			path = filepath.ToSlash(path)
			return devwatch.DefaultIgnore(path) ||
				strings.Contains(path, "/static/") || strings.HasPrefix(path, "static/")
		},
		Rebuild: rebuild,
		Command: devserverBin,
		Env:     append(os.Environ(), "DEV=1"),
	})
	if err != nil {
		log.Fatal(err)
	}
}
