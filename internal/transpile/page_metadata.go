package transpile

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Page metadata annotations are doc-comment lines directly above a
// page component's func declaration -- one key per line, each its own
// marker, deliberately shaped differently from the other two comment
// conventions already in this codebase so the three don't get
// confused for one another:
//
//   - a tool directive (//go:generate, //encore:api) triggers an
//     external command or build-time codegen;
//   - //language=SQL (see editors/vscode-gox) is a pure editor hint
//     with zero effect on the compiled program;
//   - @title/@stylesheets/@wasm, below, are neither: they're
//     informational metadata about the function that a real tool
//     (goxc) does turn into code (a gox.Document{}.Render() wrapper),
//     which is why they get a third shape instead of overloading
//     either existing one. The @-prefixed, space-separated form
//     mirrors swaggo/swag's `// @Summary`/`// @Router` handler
//     annotations -- genuine, widely-used Go prior art for exactly
//     this kind of per-function informational comment, unlike an
//     invented bare `@` marker.
const (
	titleMarker       = "@title "
	stylesheetsMarker = "@stylesheets "
	// wasmMarker names the URL a compiled WASM client is served from
	// (e.g. "/static/main.wasm"), not a filesystem path: unlike
	// @stylesheets, which reads a file's content at transpile time,
	// the .wasm binary doesn't exist yet when its SSR page is
	// transpiled -- it's built separately, from a different package,
	// typically after this page's own build. goxc only needs to know
	// where it'll be served from to generate the bootstrap <script>
	// tags.
	wasmMarker = "@wasm "
)

// defaultTitleIdent is the package-level identifier @title falls back
// to when a page component doesn't specify one: the caller declares
// `var DefaultTitle = "..."` once anywhere in the package (in main.go
// or alongside the pages themselves) and every annotated page without
// its own @title picks it up. gox does not declare it itself -- same
// "trust the Go compiler" stance as the JSON typed-import annotation:
// if it's never declared, the generated code fails to compile with an
// ordinary undefined-identifier error.
const defaultTitleIdent = "DefaultTitle"

// processPageMetadata scans src (already past tag expansion, so it's
// syntactically valid Go) for func declarations annotated with
// @title/@stylesheets/@wasm doc comments and rewrites their return
// statements to wrap the returned gox.Node in a gox.Document{}.Render()
// call: each stylesheet's file content is injected into Head via
// gox.Style, and @wasm appends the wasm_exec.js loader plus an
// WebAssembly.instantiateStreaming bootstrap script after the
// function's own returned content -- the same two <script> tags every
// WASM-consuming page in this repo otherwise had to hand-write. baseDir
// resolves @stylesheets paths the same way resource imports do:
// relative to the .gox file's own directory.
//
// A cheap substring pre-check means files that never use any of the
// three markers are returned unchanged without paying for a parse.
func processPageMetadata(filename, baseDir, src string) (string, error) {
	if !strings.Contains(src, titleMarker) && !strings.Contains(src, stylesheetsMarker) && !strings.Contains(src, wasmMarker) {
		return src, nil
	}

	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, filename, src, goparser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("%s: parsing generated Go to apply page metadata: %w", filename, err)
	}

	var edits []returnEdit

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil || fn.Body == nil {
			continue
		}

		title, stylesheets, wasmURL, found := parsePageDoc(fn.Doc)
		if !found {
			continue
		}

		if len(stylesheets) > 0 {
			return "", fmt.Errorf("%s: @stylesheets on func %s is no longer supported: import the stylesheet instead, `import _ \"<module path>/%s\"`, and gox.Document includes it", filename, fn.Name.Name, stylesheets[0])
		}
		var headExprs []string

		titleSrc := defaultTitleIdent
		if title != "" {
			titleSrc = strconv.Quote(title)
		}
		headSrc := "[]gox.Node{" + strings.Join(headExprs, ", ") + "}"

		bodyWrap := func(original string) string { return original }
		if wasmURL != "" {
			wasmExecURL := path.Join(path.Dir(wasmURL), "wasm_exec.js")
			bootstrapJS := fmt.Sprintf(`
const go = new Go();
WebAssembly.instantiateStreaming(fetch(%s), go.importObject).then((result) => {
	go.run(result.instance);
});
`, strconv.Quote(wasmURL))
			bodyWrap = func(original string) string {
				return fmt.Sprintf(
					`gox.Frag(%s, gox.El("script", []gox.Attr{gox.A("src", %s)}), gox.El("script", nil, gox.Raw(%s)))`,
					original, strconv.Quote(wasmExecURL), strconv.Quote(bootstrapJS),
				)
			}
		}

		wrapped := wrapPageReturns(fset, src, fn.Body, titleSrc, headSrc, bodyWrap)
		if len(wrapped) == 0 {
			return "", fmt.Errorf("%s: func %s has an @title/@stylesheets/@wasm annotation but no single-value return statement to wrap -- does it return exactly one gox.Node?", filename, fn.Name.Name)
		}
		edits = append(edits, wrapped...)
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out strings.Builder
	last := 0
	for _, e := range edits {
		out.WriteString(src[last:e.start])
		out.WriteString(e.replacement)
		last = e.end
	}
	out.WriteString(src[last:])
	return out.String(), nil
}

// parsePageDoc scans a func's doc comment for @title/@stylesheets/
// @wasm lines, leaving ordinary prose lines alone. found is false if
// none of the markers appear, meaning the function is left untouched.
func parsePageDoc(doc *ast.CommentGroup) (title string, stylesheets []string, wasmURL string, found bool) {
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		switch {
		case strings.HasPrefix(text, titleMarker):
			title = strings.TrimSpace(strings.TrimPrefix(text, titleMarker))
			found = true
		case strings.HasPrefix(text, stylesheetsMarker):
			stylesheets = append(stylesheets, strings.TrimSpace(strings.TrimPrefix(text, stylesheetsMarker)))
			found = true
		case strings.HasPrefix(text, wasmMarker):
			wasmURL = strings.TrimSpace(strings.TrimPrefix(text, wasmMarker))
			found = true
		}
	}
	return title, stylesheets, wasmURL, found
}

type returnEdit struct {
	start, end  int
	replacement string
}

// wrapPageReturns locates every return statement in body's own
// top-level control flow -- ifs, fors, switches, blocks -- and
// reports a text edit wrapping each one's expression in
// gox.Document{Title: ..., Head: ..., Body: wrapBody(<original
// expression text, untouched>)}.Render(). It never descends into a
// nested func literal: those returns belong to that closure, not to
// the page component itself, and must be left alone (e.g. the
// immediately-invoked closures this codebase already uses for
// rendering a []gox.Node from a loop).
func wrapPageReturns(fset *token.FileSet, src string, body *ast.BlockStmt, titleSrc, headSrc string, wrapBody func(string) string) []returnEdit {
	var edits []returnEdit
	var walkStmt func(ast.Stmt)
	walkStmts := func(list []ast.Stmt) {
		for _, s := range list {
			walkStmt(s)
		}
	}
	walkStmt = func(s ast.Stmt) {
		switch st := s.(type) {
		case *ast.ReturnStmt:
			if len(st.Results) != 1 {
				return
			}
			start := fset.Position(st.Results[0].Pos()).Offset
			end := fset.Position(st.Results[0].End()).Offset
			original := src[start:end]
			replacement := fmt.Sprintf("gox.Document{Title: %s, Head: %s, Body: %s}.Render()", titleSrc, headSrc, wrapBody(original))
			edits = append(edits, returnEdit{start: start, end: end, replacement: replacement})
		case *ast.BlockStmt:
			walkStmts(st.List)
		case *ast.IfStmt:
			walkStmt(st.Body)
			if st.Else != nil {
				walkStmt(st.Else)
			}
		case *ast.ForStmt:
			walkStmt(st.Body)
		case *ast.RangeStmt:
			walkStmt(st.Body)
		case *ast.SwitchStmt:
			walkStmts(st.Body.List)
		case *ast.TypeSwitchStmt:
			walkStmts(st.Body.List)
		case *ast.CaseClause:
			walkStmts(st.Body)
		case *ast.SelectStmt:
			walkStmts(st.Body.List)
		case *ast.CommClause:
			walkStmts(st.Body)
		case *ast.LabeledStmt:
			walkStmt(st.Stmt)
		}
	}
	walkStmts(body.List)
	return edits
}
