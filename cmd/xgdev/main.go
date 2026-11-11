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
		log.Fatal("xgdev: no server command given -- pass it after a literal --, e.g. `xgdev -build ... -- ./myserver`")
	}
	if len(builds) == 0 {
		log.Fatal("xgdev: no -build steps given -- at least one is required to produce something to run")
	}
	command, cmdArgs := args[0], args[1:]

	rebuild := func() bool {
		for _, step := range builds {
			cmd := exec.Command("sh", "-c", step)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Printf("xgdev: build step %q failed: %v", step, err)
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
