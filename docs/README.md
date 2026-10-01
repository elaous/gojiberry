# Generated API docs

Plain-text `go doc -all` output for each public package, regenerate with:

```sh
go doc -all ./gox > docs/gox.txt
go doc -all ./components > docs/components.txt
go doc -all ./devreload > docs/devreload.txt
go doc -all ./devwatch > docs/devwatch.txt
GOOS=js GOARCH=wasm go doc -all ./domrt > docs/domrt.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/browser > docs/domrt-browser.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/html > docs/domrt-html.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/svg > docs/domrt-svg.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/sw > docs/domrt-sw.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/worker > docs/domrt-worker.txt
```

`domrt`, `domrt/browser`, `domrt/html`, `domrt/svg`, `domrt/sw`, and
`domrt/worker` are `js`/`wasm`-only packages, hence `GOOS=js GOARCH=wasm`;
`gox` and `components` are plain Go.

Hand-written notes:

- `gox-canvas.md` -- how gox (function props, generic components, a
  possible `goxcanvas` renderer) can drive the canvas components.
