# Generated API docs

Plain-text `go doc -all` output for each public package, regenerate with:

```sh
go doc -all ./gox > docs/gox.txt
go doc -all ./components > docs/components.txt
GOOS=js GOARCH=wasm go doc -all ./domrt > docs/domrt.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/browser > docs/domrt-browser.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/html > docs/domrt-html.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/svg > docs/domrt-svg.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/sw > docs/domrt-sw.txt
```

`domrt`, `domrt/browser`, `domrt/html`, `domrt/svg`, and `domrt/sw` are
`js`/`wasm`-only packages, hence `GOOS=js GOARCH=wasm`; `gox` and
`components` are plain Go.
