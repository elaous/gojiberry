# gox VS Code extension (v1: syntax highlighting only)

Provides `.gox` file association and syntax highlighting: real Go
tokenization (via VS Code's own bundled Go grammar or the
[golang.go](https://marketplace.visualstudio.com/items?itemName=golang.go)
extension, whichever provides `source.go`) plus `<Tag>`/`{expr}`
highlighting layered on top via a TextMate **injection grammar**. Also
includes a second, general-purpose injection — not gox-specific, works
in any `.go` file — for `//language=`-annotated embedded-language raw
strings; see below.

## `//language=`-annotated embedded strings (any `.go` file)

A raw string literal that opens with `//language=<lang>`
(case-insensitive) — JetBrains' own **Language Injection** convention
from GoLand/IntelliJ, reused as-is here rather than inventing a new
spelling — on the **same line** as the backtick gets that language's
syntax highlighting for the rest of its content:

```go
return []byte(`//language=HTML
	<div class="greeting">hello</div>
`)

query := `//language=SQL
	SELECT * FROM users WHERE id = 1
`
```

There's no Go precedent for anything else here: `@` isn't a Go token
at all outside a string or comment (`@foo` is a compile error,
`invalid character U+0040 '@'`), so an `@`-prefixed marker would have
been a made-up spelling with no prior art in this language or any
other. `//language=` already means exactly this in a widely-used
toolchain, so this extension just recognizes that convention instead
of inventing its own. It's the same idea `es6-string-html`/
`vscode-styled-components` use for JS template literals, or the
`/* GraphQL */` magic-comment convention — here for Go raw strings,
since Go has no tagged-template syntax to hook into. Supported today:
`html`, `css`, `sql`, `json`.

**The marker must share a line with the opening backtick.** VS Code's
tokenizer processes one line at a time, so a `begin` pattern's
lookahead can never see content on the *next* line — there's no way to
detect a marker placed on its own line below the backtick; that was
the first design tried here, confirmed broken by tokenizing a sample
file and finding no embedded-language scopes appeared at all before
switching to the same-line requirement. This means JetBrains' own
placement (marker on the line *before* the string) doesn't work here —
this extension recognizes only a same-line spelling, not a full
reimplementation of JetBrains' convention.

This works in plain `.go` files as much as `.gox` ones — it's
registered as its own injection (`syntaxes/embedded-highlight.tmLanguage.json`,
`injectTo: ["source.go", "source.gox"]`), independent of the gox tag
grammar.

## Why an injection grammar, not just a combined pattern list

The obvious-looking approach — `"patterns": [{"include": "#tag"},
{"include": "source.go"}]` at the grammar's top level — only wins the
match race at the outermost scope. Once Go's own grammar pushes a
nested context (entering a function body via `{`, for instance), that
nested context has its own self-contained pattern set with no
knowledge of a sibling top-level rule, so tags inside a function body
silently stop highlighting. A `injectionSelector: "L:source.gox"`
grammar reapplies at *every* nesting depth instead, which is what
`syntaxes/gox-tags.tmLanguage.json` (registered via `injectTo` in
`package.json`) does. `test/tokenize.js` exercises exactly this case
against the repo's real example files.

The selector is actually `"L:source.gox - comment"`, not just
`"L:source.gox"` -- without the exclusion, a plain comment mentioning
a bare `<Word>`-shaped sequence with no closing tag nearby (e.g. "this
page doesn't write either `<script>` tag itself", a real doc comment
that used to trigger exactly this) gets misread as an unterminated
tag-open. Since `tagNamed`'s own pattern list doesn't include
`source.go`, everything after that point -- not just tags, ordinary
`func`/identifier/type highlighting too -- silently stops working for
the rest of the file, because the tokenizer is stuck waiting for a
`</script>` that never comes.  `test/tag-in-comment-test.js` is a
regression test for exactly this against
`test/fixtures/tag-mention-in-comment.gox`.

## Known limitations

- Tag detection in the grammar is a heuristic (`<` immediately
  followed by a letter, ended by a backreference to its own opening
  name) — it does not replicate goxc's real expression-start
  disambiguation. A tightly-spaced `a<B` with no surrounding spaces
  can be miscolored as a tag-open; idiomatic (gofmt'd) Go always
  spaces binary operators, so this is rare in practice. TSX's own
  grammar has the same class of limitation for the same reason.
- No language server: no completions, hover, go-to-definition, or
  diagnostics. That would need an LSP proxy translating between
  `.gox` source positions and `gopls`'s view of the generated `.go`
  file, which in turn needs the transpiler to emit `//line` directives
  (a documented gap in the main README) — a natural follow-up once
  that lands.
- `<`/`>` are intentionally left out of `language-configuration.json`'s
  auto-closing pairs: statically auto-closing every `<` you type would
  make ordinary comparison operators (`a < b`) infuriating to write,
  since a config file can't know intent at keystroke time the way the
  transpiler's real parser does.
- `//language=` only recognizes a marker on the same line as the
  opening backtick, for the line-at-a-time tokenizer reason above —
  not a stylistic choice.

## Verifying grammar changes

```
cd test
npm install
npm run fetch-go-grammar         # downloads VS Code's bundled Go grammar (not redistributed here)
npm run fetch-embedded-grammars  # downloads its HTML/CSS/SQL/JSON grammars too
npm test                         # tokenize.js: every examples/**/*.gox has Go
                                  # keywords and (where present) tags, including
                                  # deep inside nested blocks
                                  # highlight-test.js: fixtures/highlight-sample.go's
                                  # four //language=-annotated strings each embed
                                  # their language's grammar, and a plain,
                                  # unannotated raw string stays plain Go
```

## Installing locally

Symlink or copy this directory into your VS Code extensions folder
(`~/.vscode/extensions/gox-language-0.1.0`) and reload the window, or
package it with `vsce package` once you're ready to distribute it.
