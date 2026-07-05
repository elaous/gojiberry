// Command goxdev is a generic front end for devwatch.Run: it lets a gox
// project opt into the watch/rebuild/restart dev loop from the command
// line, with no Go code of its own to write (examples/canvas-app's own
// cmd/devwatch predates this and still uses devwatch.Run directly,
// since it needs a temp-file build path devwatch's -build strings alone
// don't cover; goxdev is for the common case that doesn't).
//
// Each -build step runs as a shell command (via `sh -c`), in the order
// given; the first one to fail stops that rebuild attempt and leaves
// the previously-running server alone. Everything after a literal "--"
// is the server binary to run and restart -- run directly, not through
// a shell, so signals reach it directly.
//
// Usage:
//
//	go run github.com/elaous/gojiberry/cmd/goxdev \
//		-build "go generate ./..." \
//		-build "GOOS=js GOARCH=wasm go build -o static/main.wasm ./client" \
//		-build "go build -o /tmp/myserver ./cmd/server" \
//		-- /tmp/myserver
//
// -ignore SUBSTR (repeatable) skips any changed path containing SUBSTR,
// in addition to devwatch.DefaultIgnore's universal skips (.git,
// generated *.gox.go files) -- e.g. -ignore /static/ for a directory a
// -build step itself writes into, to avoid a rebuild feedback loop.
package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/elaous/gojiberry/devwatch"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		root    string
		builds  stringList
		ignores stringList
	)
	flag.StringVar(&root, "root", ".", "directory tree to watch")
	flag.Var(&builds, "build", "a shell command to run as a rebuild step (repeatable, run in order)")
	flag.Var(&ignores, "ignore", "skip any changed path containing this substring (repeatable)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("goxdev: no server command given -- pass it after a literal --, e.g. `goxdev -build ... -- ./myserver`")
	}
	if len(builds) == 0 {
		log.Fatal("goxdev: no -build steps given -- at least one is required to produce something to run")
	}
	command, cmdArgs := args[0], args[1:]

	rebuild := func() bool {
		for _, step := range builds {
			cmd := exec.Command("sh", "-c", step)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Printf("goxdev: build step %q failed: %v", step, err)
				return false
			}
		}
		return true
	}

	ignore := func(path string) bool {
		if devwatch.DefaultIgnore(path) {
			return true
		}
		for _, substr := range ignores {
			if strings.Contains(path, substr) {
				return true
			}
		}
		return false
	}

	err := devwatch.Run(devwatch.Config{
		Root:    root,
		Ignore:  ignore,
		Rebuild: rebuild,
		Command: command,
		Args:    cmdArgs,
	})
	if err != nil {
		log.Fatal(err)
	}
}
