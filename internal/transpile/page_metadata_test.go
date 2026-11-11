package transpile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func setupModuleWithXango(t *testing.T) (pkgDir string) {
	t.Helper()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
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
	dir := setupModuleWithXango(t)
	writeFile(t, dir, "App.css", "body { color: red; }")
	src := `package main

import (
	"fmt"
	"os"

	_ "example.com/testmod/pkg/App.css"
	"github.com/elaous/gojiberry/xango"
)

// @title bob
func Page() xango.Node {
	return xango.El("h1", nil, xango.Text("hello"))
}

func main() {
	got, err := xango.RenderString(Page())
	if err != nil {
		panic(err)
	}
	want := ` + "`" + `<html><head><title>bob</title><style data-xango-style="example.com/testmod/pkg/App.css">body { color: red; }</style></head><body><h1>hello</h1></body></html>` + "`" + `
	if got != want {
		panic(fmt.Sprintf("got %q want %q", got, want))
	}
	os.Exit(0)
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `xango.Document{Title: "bob"`) {
		t.Errorf("expected a xango.Document wrapper with the literal title, got:\n%s", got)
	}
	if !strings.Contains(got, `xango.RegisterStyle("example.com/testmod/pkg/App.css"`) {
		t.Errorf("expected the CSS import to register the stylesheet, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestPageMetadataFallsBackToDefaultTitleWhenOmitted(t *testing.T) {
	dir := setupModuleWithXango(t)
	src := `package main

import "github.com/elaous/gojiberry/xango"

var DefaultTitle = "fallback title"

// @wasm /static/main.wasm
func Page() xango.Node {
	return xango.Text("x")
}

func main() {
	got, err := xango.RenderString(Page())
	if err != nil {
		panic(err)
	}
	if !strings.Contains(got, "<title>fallback title</title>") {
		panic("got " + got)
	}
}
`
	src = strings.Replace(src, `import "github.com/elaous/gojiberry/xango"`, "import (\n\t\"strings\"\n\n\t\"github.com/elaous/gojiberry/xango\"\n)", 1)
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "xango.Document{Title: DefaultTitle") {
		t.Errorf("expected the omitted title to fall back to the DefaultTitle identifier, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestStylesheetsAnnotationIsRejectedWithAMigrationHint(t *testing.T) {
	dir := setupModuleWithXango(t)
	src := `package main

import "github.com/elaous/gojiberry/xango"

// @stylesheets App.css
func Page() xango.Node { return xango.Text("x") }
`
	_, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err == nil || !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), `import _ `) {
		t.Fatalf("want a migration error, got %v", err)
	}
}

func TestPageMetadataWrapsEveryTopLevelReturn(t *testing.T) {
	dir := setupModuleWithXango(t)
	src := `package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/xango"
)

var DefaultTitle = "t"

// @title conditional
func Page(empty bool) xango.Node {
	if empty {
		return xango.Text("empty")
	}
	return xango.Text("full")
}

func main() {
	for _, tc := range []struct {
		empty bool
		want  string
	}{
		{true, "<html><head><title>conditional</title></head><body>empty</body></html>"},
		{false, "<html><head><title>conditional</title></head><body>full</body></html>"},
	} {
		got, err := xango.RenderString(Page(tc.empty))
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
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "xango.Document{Title:") != 2 {
		t.Errorf("expected both branches' return statements wrapped, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

// TestPageMetadataInjectsWasmBootstrapScripts: loader and bootstrap follow the page.
func TestPageMetadataInjectsWasmBootstrapScripts(t *testing.T) {
	dir := setupModuleWithXango(t)
	src := `package main

import (
	"os"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// @title app
// @wasm /static/main.wasm
func Page() xango.Node {
	return xango.El("div", []xango.Attr{xango.A("id", "app")})
}

func main() {
	got, err := xango.RenderString(Page())
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
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `xango.A("src", "/static/wasm_exec.js")`) {
		t.Errorf("expected wasm_exec.js's URL derived from the .wasm URL's directory, got:\n%s", got)
	}
	if !strings.Contains(got, "xango.Frag(xango.El(\"div\"") {
		t.Errorf("expected the original body wrapped in a Frag alongside the scripts, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestPageMetadataDoesNotWrapNestedClosureReturns(t *testing.T) {
	dir := setupModuleWithXango(t)
	src := `package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/xango"
)

// @title list
func Page(items []string) xango.Node {
	return xango.Frag(func() []xango.Node {
		var out []xango.Node
		for _, s := range items {
			out = append(out, xango.El("li", nil, xango.Text(s)))
		}
		return out
	}()...)
}

func main() {
	got, err := xango.RenderString(Page([]string{"a", "b"}))
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
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "xango.Document{Title:") != 1 {
		t.Errorf("expected exactly one wrap (the outer return only), got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}

func TestPageMetadataLeavesUnannotatedFunctionsUntouched(t *testing.T) {
	dir := setupModule(t)
	src := `package main

import "github.com/elaous/gojiberry/xango"

// Greeting is an ordinary component with an ordinary doc comment.
func Greeting() xango.Node {
	return xango.Text("hi")
}
`
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "xango.Document") {
		t.Errorf("expected an unannotated function to be left alone, got:\n%s", out)
	}
}

func TestPageMetadataErrorsWithoutASingleValueReturn(t *testing.T) {
	dir := setupModule(t)
	src := `package main

import "github.com/elaous/gojiberry/xango"

var DefaultTitle = "t"

// @title no-return-to-wrap
func Setup() {
	_ = xango.Text("unused")
}
`
	_, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err == nil {
		t.Fatal("expected an error: no return statement to wrap")
	}
	if !strings.Contains(err.Error(), "Setup") {
		t.Errorf("expected the error to name the annotated function, got: %v", err)
	}
}

func TestStylesAnnotationChoosesStylesPerPage(t *testing.T) {
	dir := setupModuleWithXango(t)
	writeFile(t, dir, "a.css", "A{}")
	writeFile(t, dir, "b.css", "B{}")
	src := `package main

import (
	"fmt"
	"strings"

	_ "example.com/testmod/pkg/a.css"
	_ "example.com/testmod/pkg/b.css"
	"github.com/elaous/gojiberry/xango"
)

// @title all
func All() xango.Node { return xango.Text("x") }

// @title only a
// @styles a.css
func OnlyA() xango.Node { return xango.Text("x") }

// @title none
// @styles none
func None() xango.Node { return xango.Text("x") }

// @title both, by full path
// @styles example.com/testmod/pkg/a.css b.css
func Both() xango.Node { return xango.Text("x") }

func main() {
	render := func(n xango.Node) string { s, _ := xango.RenderString(n); return s }
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
	out, err := Transpile(filepath.Join(dir, "main.xg"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `Styles: []string{"a.css"}`) || !strings.Contains(got, `Styles: []string{}`) {
		t.Errorf("expected Styles fields in the generated Document, got:\n%s", got)
	}
	mustCompileAndRun(t, dir, "main.go", got)
}
