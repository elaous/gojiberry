//go:build ignore

// This file is a tokenization fixture for highlight-test.js, not a
// buildable Go source file (there's no func main here on purpose) --
// the ignore tag keeps `go build ./...` at the repo root from tripping
// over it.
package main

func htmlSnippet() []byte {
	return []byte(`// @highlight: html
		<div class="greeting">hello</div>
	`)
}

func cssSnippet() string {
	return `// @highlight: css
		body { color: red; }
	`
}

// sqlSnippet uses JetBrains' `//language=SQL` spelling instead of
// `@highlight:`, to prove both are recognized.
func sqlSnippet() string {
	return `//language=SQL
		SELECT * FROM users WHERE id = 1;
	`
}

func jsonSnippet() string {
	return `// @highlight: json
		{"title": "gojiberry"}
	`
}

// plainRawString has no @highlight marker and must keep plain Go
// raw-string coloring, proving the injection doesn't fire on every
// backtick -- only ones that open with a marker on the same line.
func plainRawString() string {
	return `
		this is just a plain raw string with no annotation
	`
}
