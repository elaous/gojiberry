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

// Page metadata: @-prefixed doc-comment lines above a page func.
const (
	titleMarker       = "@title "
	stylesheetsMarker = "@stylesheets "
	// stylesMarker limits a page to named stylesheets, or none.
	stylesMarker = "@styles "
	// wasmMarker names the URL the WASM client is served from.
	wasmMarker = "@wasm "
)

// defaultTitleIdent: the package var @title falls back to.
const defaultTitleIdent = "DefaultTitle"

func processPageMetadata(filename, baseDir, src string) (string, error) {
	if !strings.Contains(src, titleMarker) && !strings.Contains(src, stylesheetsMarker) && !strings.Contains(src, stylesMarker) && !strings.Contains(src, wasmMarker) {
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

		title, stylesheets, styles, wasmURL, found := parsePageDoc(fn.Doc)
		if !found {
			continue
		}

		if len(stylesheets) > 0 {
			return "", fmt.Errorf("%s: @stylesheets on func %s is no longer supported: import the stylesheet instead, `import _ \"<module path>/%s\"`, and xango.Document includes it", filename, fn.Name.Name, stylesheets[0])
		}
		var headExprs []string

		titleSrc := defaultTitleIdent
		if title != "" {
			titleSrc = strconv.Quote(title)
		}
		headSrc := "[]xango.Node{" + strings.Join(headExprs, ", ") + "}"
		stylesSrc := ""
		if styles != nil {
			quoted := make([]string, len(styles))
			for i, st := range styles {
				quoted[i] = strconv.Quote(st)
			}
			stylesSrc = ", Styles: []string{" + strings.Join(quoted, ", ") + "}"
		}

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
					`xango.Frag(%s, xango.El("script", []xango.Attr{xango.A("src", %s)}), xango.El("script", nil, xango.Raw(%s)))`,
					original, strconv.Quote(wasmExecURL), strconv.Quote(bootstrapJS),
				)
			}
		}

		wrapped := wrapPageReturns(fset, src, fn.Body, titleSrc, headSrc+stylesSrc, bodyWrap)
		if len(wrapped) == 0 {
			return "", fmt.Errorf("%s: func %s has an @title/@stylesheets/@wasm annotation but no single-value return statement to wrap -- does it return exactly one xango.Node?", filename, fn.Name.Name)
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

// parsePageDoc finds the @-markers in a func doc; false if none.
func parsePageDoc(doc *ast.CommentGroup) (title string, stylesheets, styles []string, wasmURL string, found bool) {
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		switch {
		case strings.HasPrefix(text, titleMarker):
			title = strings.TrimSpace(strings.TrimPrefix(text, titleMarker))
			found = true
		case strings.HasPrefix(text, stylesheetsMarker):
			stylesheets = append(stylesheets, strings.TrimSpace(strings.TrimPrefix(text, stylesheetsMarker)))
			found = true
		case strings.HasPrefix(text, stylesMarker):
			styles = []string{} // non-nil: the page chose
			for _, f := range strings.Fields(strings.TrimPrefix(text, stylesMarker)) {
				if f != "none" {
					styles = append(styles, f)
				}
			}
			found = true
		case strings.HasPrefix(text, wasmMarker):
			wasmURL = strings.TrimSpace(strings.TrimPrefix(text, wasmMarker))
			found = true
		}
	}
	return title, stylesheets, styles, wasmURL, found
}

type returnEdit struct {
	start, end  int
	replacement string
}

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
			replacement := fmt.Sprintf("xango.Document{Title: %s, Head: %s, Body: %s}.Render()", titleSrc, headSrc, wrapBody(original))
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
