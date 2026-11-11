# Deploying a wasm app: size, compression and caching

A Go wasm app is one large file. The editor showcase's `main.wasm` (now the [OpenDocs](https://github.com/elaous/opendocs) app) is about 28 MB raw and 6.9 MB as gzip.
On a slow link or a
weak device the first load is the whole cost, so how the file is served matters as much as how it is
built. This page is the recipe for the showcase; it applies to any app built the same way. Roadmap item
82 is the backlog behind it. Nothing here needs an account or a credential: the output is a directory
of static files.

## 1. Build and compress

The OpenDocs repository builds its app into a directory (see its README for the command).
That build produces `main.wasm`, copies `index.html` and `wasm_exec.js` beside it, and runs
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

[`nginx.conf`](https://github.com/elaous/opendocs/blob/main/nginx.conf) in the OpenDocs repository is the
whole server block: `gzip_static on` (sends `main.wasm.gz`), `gzip on` for what has no `.gz`, the wasm
type inside a `location` so the other types stay as nginx knows them, `Cache-Control: no-cache`, and a
404 for missing scripts. For `.br` files add `brotli_static on;` and an nginx with the
[ngx_brotli](https://github.com/google/ngx_brotli) module; the stock `nginx` image has none, which is why
the Dockerfile writes only `.gz`.

The Docker image does all of it:

```
docker build -t opendocs https://github.com/elaous/opendocs.git
docker run --rm -p 8080:80 opendocs
```

(`nginx.conf` and the Caddy block below are run by tests against the real servers, with a directory that
`wasmdist` compressed: `TestShowcaseNginxRecipe` and `TestShowcaseCaddyRecipe` in `cmd/wasmdist` check the wasm
type, the precompressed file sent as it is (and, for Caddy, the `.br` file first when the browser takes it), the file
itself otherwise, `Cache-Control`, the 304 for a repeat visit, the 404 for a script that is not there and the start page
for an app path. They skip when the server is not installed, unless `GOJIBERRY_REQUIRE_NGINX` or
`GOJIBERRY_REQUIRE_CADDY` is set, which the CI workflow does; `GOJIBERRY_CADDY` names a Caddy that is not on the
path. nginx's `brotli_static` needs the ngx_brotli module, which was not installed, so the `.br` path of nginx is not
run. The Dockerfile itself was not built: there is no Docker daemon here.)

### Caddy

```
example.com {
	root * /srv/showcase
	encode zstd gzip       # for files that have no precompressed sibling
	@scripts path_regexp ^.+\.(js|css|wasm|json|png|svg|ico)$
	handle @scripts {
		file_server {
			precompressed br gzip
		}
	}
	handle {
		try_files {path} /index.html
		file_server
	}
	header Cache-Control "no-cache"
}
```

The first `handle` is for files with an extension: they are sent with their `.br` or `.gz` sibling when the browser takes
one, and a file that is not there is a 404. That matters: a plain `try_files {path} /index.html` would answer a missing
script (a wrong `main.wasm` path, a `sw.js` that was not deployed) with the start page and a 200, and the browser would
then fail on HTML where it expects a program. The second `handle` is for app paths such as `/d/report`, which get the
start page. Caddy knows `.wasm`. `precompressed br gzip` sends `main.wasm.br` or `.gz` as they are.

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

### The second program (the file formats)

The showcase is two programs: `main.wasm` (about 26 MB, 6.4 MB gzipped), which every visitor downloads, and
`files.wasm` (about 9.5 MB, 2.7 MB gzipped) with `files-worker.js`, which a web worker loads the first time a file
is imported or exported (Word, OpenDocument, PDF, RTF, EPUB, HTML). Both are built by the OpenDocs build and its
Dockerfile, both are precompressed by `wasmdist`, and nothing else is needed on the server: the page asks for
`files-worker.js` and `files.wasm` next to `index.html`.

### The service worker (repeat visits and no connection)

`wasmdist` also writes `sw.js` next to `index.html` (`-sw=false` leaves it out), and the showcase page registers
it. It keeps `index.html`, `wasm_exec.js` and `main.wasm` in a cache named for a hash of their contents, so a
repeat visit starts with no request for the 30 MB program, and the app opens without a connection (its documents
are in the browser; the file formats too once they have been used). A new build is a new `sw.js` (the version is in its text), the browser installs it, and the
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

## What is not done

Roadmap 82's five steps are all in, and the nginx and Caddy settings above are run by tests against the real servers
(`cmd/wasmdist`). What is not done, because it needs something this repository cannot supply:

- The memory that low-memory mode saves was measured in the test browser, not on a real small phone: that needs the
  device and a person to run it.
- nginx's `brotli_static` (the `.br` files) needs the ngx_brotli module, which the nginx used for the tests does not
  have, so that one path is documented and not run.
- Whether a particular host (a CDN, a managed static host) sends the precompressed files as they are is for that host
  to answer: the checks in step 1 (`curl -sI -H 'Accept-Encoding: gzip' .../main.wasm`) tell you in a minute.
