package transpile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cssProgram = `package main

import (
	"fmt"
	"os"

	%s
	"github.com/elaous/gojiberry/xango"
)

func main() {
	got := xango.RegisteredStyles()
	%s
	_ = fmt.Sprint
	_ = os.Exit
}
`

func TestBlankCSSImportRegistersTheStylesheet(t *testing.T) {
	dir := setupModuleWithXango(t)
	writeFile(t, dir, "button.css", ".b { color: red; }")
	if err := os.MkdirAll(filepath.Join(dir, "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "theme/dark.css", ".d { color: white; }")
	src := strings.Replace(cssProgram, "%s", `_ "example.com/testmod/pkg/button.css"
	_ "./theme/dark.css"
	_ "example.com/testmod/pkg/button.css"`, 1)
	src = strings.Replace(src, "%s", `if len(got) != 2 || got[0].ID != "example.com/testmod/pkg/button.css" || got[0].CSS != ".b { color: red; }" ||
		got[1].ID != "example.com/testmod/pkg/theme/dark.css" || got[1].CSS != ".d { color: white; }" {
		panic(fmt.Sprintf("registered: %+v", got))
	}`, 1)
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "button.css\"\n") && strings.Count(string(out), `_ "example.com/testmod/pkg/button.css"`) > 0 {
		t.Errorf("a resource import must not stay in the import block:\n%s", out)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestNamedCSSImportBindsTheText(t *testing.T) {
	dir := setupModuleWithXango(t)
	writeFile(t, dir, "x.css", "a{b:c}")
	src := `package main

import (
	"fmt"
	theme "example.com/testmod/pkg/x.css"
	"github.com/elaous/gojiberry/xango"
)

func main() {
	if theme != "a{b:c}" || len(xango.RegisteredStyles()) != 0 {
		panic(fmt.Sprintf("theme=%q registered=%v", theme, xango.RegisteredStyles()))
	}
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}

func TestCSSImportErrors(t *testing.T) {
	dir := setupModuleWithXango(t)
	writeFile(t, dir, "x.css", "a{}")
	for name, tc := range map[string]struct{ spec, want string }{
		"no name":      {`"example.com/testmod/pkg/x.css"`, "needs a name or _"},
		"missing file": {`_ "example.com/testmod/pkg/nope.css"`, "nope.css"},
		"not a module": {`_ "example.org/other/x.css"`, "required module"},
	} {
		src := "package main\n\nimport (\n\t" + tc.spec + "\n)\n\nfunc main() {}\n"
		_, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
	}
}

// A stylesheet in a module the current one requires -- found through the
// go command, so replace directives and the module cache both work.
func TestCSSImportFromARequiredModule(t *testing.T) {
	dir := setupModuleWithXango(t)
	lib := t.TempDir()
	writeFile(t, lib, "go.mod", "module example.com/lib\n\ngo 1.21\n")
	if err := os.MkdirAll(filepath.Join(lib, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, lib, "ui/card.css", ".card{}")
	modFile := filepath.Join(filepath.Dir(dir), "go.mod")
	data, err := os.ReadFile(modFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\nrequire example.com/lib v0.0.0\n\nreplace example.com/lib => "+lib+"\n")...)
	if err := os.WriteFile(modFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(cssProgram, "%s", `_ "example.com/lib/ui/card.css"`, 1)
	src = strings.Replace(src, "%s", `if len(got) != 1 || got[0].ID != "example.com/lib/ui/card.css" || got[0].CSS != ".card{}" {
		panic(fmt.Sprintf("registered: %+v", got))
	}`, 1)
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	mustCompileAndRun(t, dir, "main.go", string(out))
}
