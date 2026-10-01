package transpile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// setupModuleWithGox is setupModule plus a replace directive pointing
// back at this repo, for tests whose generated code imports the real
// gox package (gox.Document{}.Render() needs a real gox.Node tree to
// wrap, not just a resource-import string binding).
func setupModuleWithGox(t *testing.T) (pkgDir string) {
	t.Helper()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// The test module's own `go` directive must be at least as new as
	// gojiberry's (its go.mod declares go 1.24.7): otherwise Go's
	// minimum-version-selection refuses to build ("updates to go.mod
	// needed") even though the replace is purely a local filesystem
	// path with no real version to resolve. Read the real directive
	// instead of hardcoding a version that could drift out of sync.
	repoGoMod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	goDirective := goDirectiveRE.FindStringSubmatch(string(repoGoMod))
	if goDirective == nil {
		t.Fatalf("could not find a `go` directive in %s/go.mod", repoRoot)
	}
	root := t.TempDir()
	goMod := "module example.com/testmod\n\ngo " + goDirective[1] + "\n\nrequire github.com/elaous/gojiberry v0.0.0\n\nreplace github.com/elaous/gojiberry => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir = filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return pkgDir
}

var goDirectiveRE = regexp.MustCompile(`(?m)^go (\S+)$`)

func TestPageMetadataWrapsReturnInDocument(t *testing.T) {
	dir := setupModuleWithGox(t)
	writeFile(t, dir, "App.css", "body { color: red; }")
	src := `package main

import (
	"fmt"
	"os"

	_ "example.com/testmod/pkg/App.css"
	"github.com/elaous/gojiberry/gox"
)

// @title bob
func Page() gox.Node {
	return gox.El("h1", nil, gox.Text("hello"))
}

func main() {
	got, err := gox.RenderString(Page())
	if err != nil {
		panic(err)
	}
	want := ` + "`" + `<html><head><title>bob</title><style data-gox-style="example.com/testmod/pkg/App.css">body { color: red; }</style></head><body><h1>hello</h1></body></html>` + "`" + `
	if got != want {
		panic(fmt.Sprintf("got %q want %q", got, want))
	}
	os.Exit(0)
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `gox.Document{Title: "bob"`) {
		t.Errorf("expected a gox.Document wrapper with the literal title, got:\n%s", got)
	}
	if !strings.Contains(got, `gox.RegisterStyle("example.com/testmod/pkg/App.css"`) {
		t.Errorf("expected the CSS import to register the stylesheet, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestPageMetadataFallsBackToDefaultTitleWhenOmitted(t *testing.T) {
	dir := setupModuleWithGox(t)
	src := `package main

import "github.com/elaous/gojiberry/gox"

var DefaultTitle = "fallback title"

// @wasm /static/main.wasm
func Page() gox.Node {
	return gox.Text("x")
}

func main() {
	got, err := gox.RenderString(Page())
	if err != nil {
		panic(err)
	}
	if !strings.Contains(got, "<title>fallback title</title>") {
		panic("got " + got)
	}
}
`
	src = strings.Replace(src, `import "github.com/elaous/gojiberry/gox"`, "import (\n\t\"strings\"\n\n\t\"github.com/elaous/gojiberry/gox\"\n)", 1)
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "gox.Document{Title: DefaultTitle") {
		t.Errorf("expected the omitted title to fall back to the DefaultTitle identifier, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestStylesheetsAnnotationIsRejectedWithAMigrationHint(t *testing.T) {
	dir := setupModuleWithGox(t)
	src := `package main

import "github.com/elaous/gojiberry/gox"

// @stylesheets App.css
func Page() gox.Node { return gox.Text("x") }
`
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil || !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), `import _ `) {
		t.Fatalf("want a migration error, got %v", err)
	}
}

func TestPageMetadataWrapsEveryTopLevelReturn(t *testing.T) {
	dir := setupModuleWithGox(t)
	src := `package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/gox"
)

var DefaultTitle = "t"

// @title conditional
func Page(empty bool) gox.Node {
	if empty {
		return gox.Text("empty")
	}
	return gox.Text("full")
}

func main() {
	for _, tc := range []struct {
		empty bool
		want  string
	}{
		{true, "<html><head><title>conditional</title></head><body>empty</body></html>"},
		{false, "<html><head><title>conditional</title></head><body>full</body></html>"},
	} {
		got, err := gox.RenderString(Page(tc.empty))
		if err != nil {
			panic(err)
		}
		if got != tc.want {
			panic(fmt.Sprintf("empty=%v: got %q want %q", tc.empty, got, tc.want))
		}
	}
	os.Exit(0)
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "gox.Document{Title:") != 2 {
		t.Errorf("expected both branches' return statements wrapped, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

// TestPageMetadataInjectsWasmBootstrapScripts covers @wasm: it should
// append the wasm_exec.js loader and an instantiateStreaming bootstrap
// script after the page's own content -- the exact boilerplate every
// WASM-consuming example in this repo otherwise hand-writes -- with
// wasm_exec.js's URL derived from the .wasm URL's own directory.
func TestPageMetadataInjectsWasmBootstrapScripts(t *testing.T) {
	dir := setupModuleWithGox(t)
	src := `package main

import (
	"os"
	"strings"

	"github.com/elaous/gojiberry/gox"
)

// @title app
// @wasm /static/main.wasm
func Page() gox.Node {
	return gox.El("div", []gox.Attr{gox.A("id", "app")})
}

func main() {
	got, err := gox.RenderString(Page())
	if err != nil {
		panic(err)
	}
	if !strings.Contains(got, ` + "`" + `<div id="app"></div><script src="/static/wasm_exec.js"></script><script>` + "`" + `) {
		panic("scripts not appended right after the page's own content: " + got)
	}
	if !strings.Contains(got, ` + "`" + `WebAssembly.instantiateStreaming(fetch("/static/main.wasm")` + "`" + `) {
		panic("bootstrap script doesn't fetch the annotated wasm URL: " + got)
	}
	if !strings.HasSuffix(got, "</script></body></html>") {
		panic("expected the bootstrap script to be the last thing in body: " + got)
	}
	os.Exit(0)
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `gox.A("src", "/static/wasm_exec.js")`) {
		t.Errorf("expected wasm_exec.js's URL derived from the .wasm URL's directory, got:\n%s", got)
	}
	if !strings.Contains(got, "gox.Frag(gox.El(\"div\"") {
		t.Errorf("expected the original body wrapped in a Frag alongside the scripts, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

// TestPageMetadataDoesNotWrapNestedClosureReturns covers the idiom
// this codebase already uses for rendering a []gox.Node from a loop
// (see examples/ssr-demo's ItemList): the immediately-invoked
// closure's own "return" produces a []gox.Node, not a gox.Node, and
// must not be rewritten into a gox.Document -- only the annotated
// function's own top-level return should be.
func TestPageMetadataDoesNotWrapNestedClosureReturns(t *testing.T) {
	dir := setupModuleWithGox(t)
	src := `package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/gox"
)

// @title list
func Page(items []string) gox.Node {
	return gox.Frag(func() []gox.Node {
		var out []gox.Node
		for _, s := range items {
			out = append(out, gox.El("li", nil, gox.Text(s)))
		}
		return out
	}()...)
}

func main() {
	got, err := gox.RenderString(Page([]string{"a", "b"}))
	if err != nil {
		panic(err)
	}
	want := "<html><head><title>list</title></head><body><li>a</li><li>b</li></body></html>"
	if got != want {
		panic(fmt.Sprintf("got %q want %q", got, want))
	}
	os.Exit(0)
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "gox.Document{Title:") != 1 {
		t.Errorf("expected exactly one wrap (the outer return only), got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestPageMetadataLeavesUnannotatedFunctionsUntouched(t *testing.T) {
	dir := setupModule(t)
	src := `package main

import "github.com/elaous/gojiberry/gox"

// Greeting is an ordinary component with an ordinary doc comment.
func Greeting() gox.Node {
	return gox.Text("hi")
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "gox.Document") {
		t.Errorf("expected an unannotated function to be left alone, got:\n%s", out)
	}
}

func TestPageMetadataErrorsWithoutASingleValueReturn(t *testing.T) {
	dir := setupModule(t)
	src := `package main

import "github.com/elaous/gojiberry/gox"

var DefaultTitle = "t"

// @title no-return-to-wrap
func Setup() {
	_ = gox.Text("unused")
}
`
	_, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err == nil {
		t.Fatal("expected an error: no return statement to wrap")
	}
	if !strings.Contains(err.Error(), "Setup") {
		t.Errorf("expected the error to name the annotated function, got: %v", err)
	}
}

func TestStylesAnnotationChoosesStylesPerPage(t *testing.T) {
	dir := setupModuleWithGox(t)
	writeFile(t, dir, "a.css", "A{}")
	writeFile(t, dir, "b.css", "B{}")
	src := `package main

import (
	"fmt"
	"strings"

	_ "example.com/testmod/pkg/a.css"
	_ "example.com/testmod/pkg/b.css"
	"github.com/elaous/gojiberry/gox"
)

// @title all
func All() gox.Node { return gox.Text("x") }

// @title only a
// @styles a.css
func OnlyA() gox.Node { return gox.Text("x") }

// @title none
// @styles none
func None() gox.Node { return gox.Text("x") }

// @title both, by full path
// @styles example.com/testmod/pkg/a.css b.css
func Both() gox.Node { return gox.Text("x") }

func main() {
	render := func(n gox.Node) string { s, _ := gox.RenderString(n); return s }
	has := func(s, sheet string) bool { return strings.Contains(s, sheet) }
	all, a, none, both := render(All()), render(OnlyA()), render(None()), render(Both())
	if !has(all, "A{}") || !has(all, "B{}") {
		panic("all: " + all)
	}
	if !has(a, "A{}") || has(a, "B{}") {
		panic("only a: " + a)
	}
	if has(none, "A{}") || has(none, "B{}") {
		panic("none: " + none)
	}
	if !has(both, "A{}") || !has(both, "B{}") {
		panic("both: " + both)
	}
	fmt.Println("ok")
}
`
	out, err := Transpile(filepath.Join(dir, "main.gox"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `Styles: []string{"a.css"}`) || !strings.Contains(got, `Styles: []string{}`) {
		t.Errorf("expected Styles fields in the generated Document, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}
