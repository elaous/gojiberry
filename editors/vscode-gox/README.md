# gox VS Code extension (v1: syntax highlighting only)

Provides `.gox` file association and syntax highlighting: real Go
tokenization (via VS Code's own bundled Go grammar or the
[golang.go](https://marketplace.visualstudio.com/items?itemName=golang.go)
extension, whichever provides `source.go`) plus `<Tag>`/`{expr}`
highlighting layered on top via a TextMate **injection grammar**.

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

## Verifying grammar changes

```
cd test
npm install
npm run fetch-go-grammar   # downloads VS Code's bundled Go grammar (not redistributed here)
npm test                   # tokenizes every examples/**/*.gox and checks
                            # both Go keywords and (where present) tags
                            # show up, including deep inside nested blocks
```

## Installing locally

Symlink or copy this directory into your VS Code extensions folder
(`~/.vscode/extensions/gox-language-0.1.0`) and reload the window, or
package it with `vsce package` once you're ready to distribute it.
