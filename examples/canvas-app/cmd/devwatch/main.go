// Command devwatch runs cmd/devserver in DEV=1 mode and rebuilds +
// restarts it whenever a source file changes, via the shared
// gojiberry/devwatch package: watches the canvas-app tree (excluding
// static/, a build-output directory this same rebuild writes into, on
// top of devwatch.DefaultIgnore's own universal skips), then on a
// change runs `make generate` + `make wasm`, builds cmd/devserver to a
// temp binary, and swaps it in for the running one.
//
// The browser side of "reload" is cmd/devserver's own use of
// gojiberry/devreload, injected only under DEV=1: it polls GET
// /__dev/version and reloads once a new devserver process (a new
// value, since the version is just time.Now() at process start) is
// listening again.
//
// This is not real hot module replacement -- a compiled .wasm binary
// is one opaque blob, not a set of hot-swappable modules -- just a
// fast watch/rebuild/restart/reload loop. That's an acceptable
// tradeoff for this particular app specifically: it's
// server-source-of-truth (Postgres via canvas.Store), so a full page
// reload always reconstructs the correct state, unlike a client-heavy
// SPA that would lose in-memory state on every rebuild.
//
// Usage (from examples/canvas-app, with Postgres already migrated):
//
//	DATABASE_URL=postgres://... go run ./cmd/devwatch
package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elaous/gojiberry/devwatch"
)

// devserverBin is where the managed devserver binary is built --
// outside the watched tree (the OS temp dir) so building it can never
// itself trigger a rebuild.
var devserverBin = filepath.Join(os.TempDir(), "gox-canvas-devwatch-server")

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

// rebuild regenerates .go from .gox, rebuilds the WASM client, and
// builds cmd/devserver to devserverBin.
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
	defer os.Remove(devserverBin)

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
