# Generated API docs

Plain-text `go doc -all` output for each public package, regenerate with:

```sh
go doc -all ./gox > docs/gox.txt
GOOS=js GOARCH=wasm go doc -all ./domrt > docs/domrt.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/browser > docs/domrt-browser.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/dom > docs/domrt-dom.txt
GOOS=js GOARCH=wasm go doc -all ./domrt/sw > docs/domrt-sw.txt
```

`domrt`, `domrt/browser`, `domrt/dom`, and `domrt/sw` are `js`/`wasm`-only
packages, hence `GOOS=js GOARCH=wasm`; `gox` is plain Go.
