# Deploying a wasm app: size, compression and caching

A Go wasm app is one large file. The editor showcase's `main.wasm` is about 27 MB raw and 6.9 MB as gzip
(the budget is in [wasm-budget.txt](wasm-budget.txt); `make wasm-size` checks it). On a slow link or a
weak device the first load is the whole cost, so how the file is served matters as much as how it is
built. This page is the recipe for the showcase; it applies to any app built the same way. Roadmap item
82 is the backlog behind it. Nothing here needs an account or a credential: the output is a directory
of static files.

## 1. Build and compress

```
make showcase-dist            # dist/showcase, or SHOWCASE_DIST=some/dir make showcase-dist
```

This builds `main.wasm`, copies `index.html` and `wasm_exec.js` beside it, and runs
[`cmd/wasmdist`](../cmd/wasmdist), which writes a `.gz` next to every compressible file (wasm,
scripts, styles, pages, JSON, SVG) of at least 1 KiB that it shrinks, at the strongest level, once:

```
wasmdist: wrote main.wasm        27319042 ->   6932654 bytes (gzip)
```

If the `brotli` tool is installed (`apt install brotli`, `brew install brotli`) it also writes a `.br`,
about 15% smaller than gzip. Brotli is worth it only if your server can send it (step 3). A compressed
file at least as new as its source is kept, so running it again after a rebuild only redoes what
changed. For an app of your own, `go run ./cmd/wasmdist <dir>` does the same to any directory.

Check what a visitor gets before you upload:

```
go run ./cmd/wasmdist -serve localhost:8080 dist/showcase
curl -sI -H 'Accept-Encoding: gzip' http://localhost:8080/main.wasm
```

The headers should show `Content-Type: application/wasm`, `Content-Encoding: gzip`, an `ETag` and
`Cache-Control: no-cache`; a second request with `If-None-Match: <that etag>` answers `304`.

## 2. What the server must do

| What | Why | Check |
| --- | --- | --- |
| `Content-Type: application/wasm` for `.wasm` | `WebAssembly.instantiateStreaming` (used by `index.html`) refuses any other type, so the browser would download the whole file before compiling it | the header above |
| Send the `.gz` or `.br` file when the browser accepts it, with `Vary: Accept-Encoding` | 27 MB becomes 6.9 MB (gzip) on the wire; compressing per request costs seconds of CPU on a file this size | `Content-Encoding` |
| `ETag` and `Cache-Control: no-cache` for files without a hash in the name | the browser keeps the file but asks each visit; the answer is a `304` with no body | `curl -I` with `If-None-Match` |
| `Cache-Control: public, max-age=31536000, immutable` only for names with a content hash | the browser does not even ask; safe only because a new build has a new name | file names |
| A missing `.js`, `.css` or `.wasm` is a 404, an app path such as `/d/report` gets `index.html` | a script that is answered with HTML fails with a confusing syntax error | `curl -I /missing.js` |

The showcase's `index.html` and `main.wasm` names carry no hash, so the first row of caching applies
(revalidate with the ETag every visit). To cache forever, rename the wasm with a hash of its content
(`main.3f9a1c.wasm`), change the name in `index.html`, serve `index.html` with `no-cache`, and mark
the hashed files immutable. `wasmserve.Options.Immutable` takes the rule.

## 3. Serving it

### Any host that can set headers and serve precompressed files

Upload the directory. Set the types and caching above in the host's own configuration.

### nginx

[`examples/canvas-editor-showcase/nginx.conf`](../examples/canvas-editor-showcase/nginx.conf) is the
whole server block: `gzip_static on` (sends `main.wasm.gz`), `gzip on` for what has no `.gz`, the wasm
type inside a `location` so the other types stay as nginx knows them, `Cache-Control: no-cache`, and a
404 for missing scripts. For `.br` files add `brotli_static on;` and an nginx with the
[ngx_brotli](https://github.com/google/ngx_brotli) module; the stock `nginx` image has none, which is why
the Dockerfile writes only `.gz`.

The Docker image does all of it:

```
docker build -f examples/canvas-editor-showcase/Dockerfile -t showcase .
docker run --rm -p 8080:80 showcase
```

(The Dockerfile, `nginx.conf` and the Caddy block below were not run in the environment this page
was written in, which has no Docker daemon, nginx or Caddy. `make showcase-dist`, `wasmdist` and
`wasmserve` were: the tests serve a compressed directory and check the type, the encoding, the 304,
the fallback and the 404.)

### Caddy

```
example.com {
	root * /srv/showcase
	encode zstd gzip       # for files that have no precompressed sibling
	file_server {
		precompressed br gzip
	}
	header Cache-Control "no-cache"
	try_files {path} /index.html
}
```

Caddy knows `.wasm`. `precompressed br gzip` sends `main.wasm.br` or `.gz` as they are.

### A Go server

[`wasmserve`](../wasmserve) is the handler for this, and what the example dev servers use. It does
everything in the table: the type, a `.br` or `.gz` sibling when it exists (otherwise it gzips once and
keeps the result), `ETag`, `Vary`, an app-path fallback and a 404 for missing files with an extension.

```go
http.Handle("/", wasmserve.Handler(os.DirFS("dist/showcase"), wasmserve.Options{Fallback: "index.html"}))
```

`go run ./cmd/wasmdist -serve :8080 dist/showcase` is the same handler as a command. For a
development server that rebuilds the wasm often, `Options{GzipLevel: gzip.BestSpeed}` keeps the first
request after a rebuild from waiting on the strongest compression.

### The service worker (repeat visits and no connection)

`wasmdist` also writes `sw.js` next to `index.html` (`-sw=false` leaves it out), and the showcase page registers
it. It keeps `index.html`, `wasm_exec.js` and `main.wasm` in a cache named for a hash of their contents, so a
repeat visit starts with no request for the 30 MB program, and the app opens without a connection (its documents
are in the browser). A new build is a new `sw.js` (the version is in its text), the browser installs it, and the
old cache is deleted when it takes over. It touches nothing else: requests to the API, sign-in and other origins
go to the network as before, and a page for an app path comes from the network too (the cached start page only
when there is none). Serve `sw.js` with `Cache-Control: no-cache` (the nginx file does, as it does for every
`.js`); a Go server gets it from `wasmserve.Options{ServiceWorker: true}`. `?nosw` in the address leaves the
worker out for that visit. To remove a worker from a browser, unregister it in the browser's application panel.

## 4. After deploying

- `curl -sI -H 'Accept-Encoding: gzip, br' https://your.host/main.wasm` shows the type, the encoding
  and the validators.
- In the browser's network panel the wasm should transfer about 7 MB (gzip) or 6 MB (brotli), and a
  reload should show it as `304` or "from cache".
- `make wasm-size` keeps the build inside the budget in [wasm-budget.txt](wasm-budget.txt); raise a number
  only on purpose.

## What is not done

Roadmap 82 plan steps 3 and 5 are still open: splitting rarely used parts out of the wasm (Go's wasm
has no dynamic loading, so this means separate binaries or a worker), and a low-memory mode. The recipe
above is step 2 (streaming compile, correct headers, compression) and the service worker is step 4.
