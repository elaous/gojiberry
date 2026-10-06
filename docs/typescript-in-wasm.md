# TypeScript in the browser from Go: tsgo measured

Status: **done** (the question is answered; nothing here is shipped). Measured on 2026-10-06 with
`github.com/microsoft/typescript-go` (the native TypeScript port, written in Go), Go 1.26.

## The question

Microsoft's TypeScript compiler is now written in Go (`tsgo`). Could Gojiberry have a TypeScript
runtime in the browser, built from it?

## Two different things

1. **A compiler** (type-check, strip types, emit JavaScript). `tsgo` is one.
2. **A runtime** (execute the JavaScript). `tsgo` is not one. A browser already has a JavaScript
   engine, so a second one in wasm only makes sense to run untrusted code under memory and time
   limits (`goja`, or QuickJS compiled to wasm); it would be slower than the browser's and add
   megabytes.

## What was built

`GOOS=js GOARCH=wasm go build ./cmd/tsgo` works with no changes (the code already treats
`GOARCH=wasm` as a case in its file-system layer). A second entry point inside the tsgo tree
(its packages are `internal/`, so nothing outside the module can import them) exposed
`transpile.TranspileModule` as a JavaScript function, run in Node with Go's `wasm_exec.js`.

| Build | Raw | Gzipped |
|---|---|---|
| full `cmd/tsgo` (compiler, language server, API) | 53.3 MB | 11.4 MB |
| full, `-ldflags="-s -w"` | 51.2 MB | 11.2 MB |
| transpile only (strip types, no check) | 30.6 MB | 7.2 MB |

For comparison the canvas-editor showcase wasm is about 31 MB today (21.5 MB with #55).

Transpiling worked: `interface`, `enum` and annotations became plain JavaScript (enums as the
usual IIFE), the `.ts` syntax error case still produced output with a diagnostic count, and a
small module takes about 2 ms after a first call of about 80 ms.

## Not measured

- A real type-check in the browser. It needs the `lib.d.ts` files (the full build embeds them)
  and a virtual file system; the wasm would be at least as large as the full build.
- Memory use of a large program, and time to first call on a phone.

## Why transpile-only is still 30 MB

The transpiler sits in the same module as the parser, binder and checker, and Go links what
the entry point reaches. A smaller build would need a fork that cuts the checker out.

## Conclusions

1. **Build time on a server: yes.** Run `tsgo` (or its Go API once it is public) to type-check and
   emit JavaScript in `go generate`, a dev server or CI. Size does not matter there.
2. **In the browser: only if TypeScript is a core feature.** Transpile-only roughly doubles what a
   visitor downloads (7 MB gzipped on top). A user-script feature should accept JavaScript, or
   transpile on the server.
3. **A sandboxed runtime for user macros** is a separate decision: `goja` in wasm. Wait for a
   concrete use case.
4. **Dependencies:** the packages are `internal/`, so using them means vendoring or forking until
   a supported Go API is published; pin a commit and expect churn.
