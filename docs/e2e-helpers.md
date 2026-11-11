# End-to-end test helpers

Two packages of this module are public so that other modules can write browser tests the same way the examples do:

| Import path | What it is |
|---|---|
| `github.com/elaous/gojiberry/e2etest` | Starts headless Chromium (through chromedp) on a page served by `httptest`, and drives it: selectors, mouse, keys, `#state` checks, axe-core, screenshots, downloads, dialogs. |
| `github.com/elaous/gojiberry/screenreader` | Reads the page's accessibility tree the way a screen reader's browse mode does. |

`e2etest/selector` is a `package main` that `e2etest` compiles to WebAssembly at run time; it MUST NOT be imported. `e2etest/axe` holds
the vendored axe-core files and is not a Go package. Neither package imports another package of this module outside the two above, so an external module needs
only `require github.com/elaous/gojiberry`.

Both packages are compiled out under `js && wasm` and MUST be imported from `_test.go` files (or other non-wasm code) only. Their exported names are a
compatibility promise: changes SHOULD be additive.

## Running

- The tests MUST run with a Go toolchain on `PATH`: `e2etest.NewSite` builds the page's `main` package with `GOOS=js GOARCH=wasm`, and the first `Open` builds the
  selector helper the same way. The Go installation MUST contain `lib/wasm/wasm_exec.js`.
- Chromium is found in this order: the `CHROMIUM` environment variable, then `PLAYWRIGHT_CHROMIUM`, then `/opt/pw-browsers/chromium`, then `chromium`,
  `chromium-browser`, `google-chrome`, `google-chrome-stable` or `chrome` on `PATH`. The first that is set or exists wins.
- With no Chromium, a test is skipped. A CI gate SHOULD set `GOJIBERRY_REQUIRE_BROWSER=1` (any non-empty value works) so that a missing Chromium fails the test instead.
- Chromium runs headless with `--no-sandbox`. A start that misses its deadline is retried, up to three attempts.

```sh
GOJIBERRY_REQUIRE_BROWSER=1 CHROMIUM=/path/to/chromium go test -count=1 ./...
```

No other environment variable is read by these two packages.

## Pages under test

- `e2etest.NewSite(t, extra...)` serves the working directory of the test: it builds `.` to `main.wasm`, copies `index.html`, `static/*` (files only) and `GOROOT/lib/wasm/wasm_exec.js`, and
  copies each file matching an `extra` glob. Paths of `/d/` and `/f/` serve `index.html`. `Site.Handle` adds handlers by path prefix.
- `e2etest.NewPageSite(t, pages)` serves fixed HTML by URL path and builds nothing.
- `Site.Open` loads `/index.html` and waits until the element `#state` exists and its text is not `(pending)`; the page SHOULD put its state as JSON in `#state`
  (`Tab.State`, `Tab.Is`). `Site.OpenURL` loads a path and waits for nothing.
- Selectors passed to `Tab` methods (`Click`, `Text`, `Count`, ...) go through the helper in `e2etest/selector`, which accepts CSS selectors plus the `:has-text(...)` and `:text-is(...)` pseudo-classes.

## Example

```go
package mypkg_test

import (
	"testing"

	"github.com/elaous/gojiberry/e2etest"
	"github.com/elaous/gojiberry/screenreader"
)

func TestPage(t *testing.T) {
	site := e2etest.NewPageSite(t, map[string]string{
		"/index.html": `<!doctype html><html lang="en"><title>t</title>
<main><h1>Title</h1><button id="b" onclick="document.getElementById('state').textContent='{&quot;n&quot;:1}'">Go</button></main>
<pre id="state">{}</pre></html>`,
	})
	tab := site.Open(t, "")
	tab.Click("#b")
	tab.OK(tab.State().Int("n") == 1, "the button ran")
	tab.NoErrors()
	if v := tab.Axe(""); len(v) != 0 {
		t.Fatal(v[0])
	}
	r := screenreader.New(t, tab.Context())
	if h, ok := r.NextHeading(1); !ok || h == "" {
		t.Fatal("no heading")
	}
}
```

## axe-core licence

`Tab.Axe` injects the vendored `e2etest/axe/axe.min.js`, which is axe-core 4.10.2, unmodified, from the npm package. axe-core is licensed under the
Mozilla Public License 2.0; the licence text is `e2etest/axe/LICENSE` and, per `e2etest/axe/README.md`, it applies to `axe.min.js` only. The rest of this module is AGPL-3.0.
The file is embedded in the `e2etest` package (`//go:embed`), so a test binary that links `e2etest` carries it. MPL-2.0 is a file-level licence: a project that
redistributes that file SHOULD keep `e2etest/axe/LICENSE` with it and MUST keep the file unmodified or publish its changes under the MPL. This is a summary, not legal advice.
