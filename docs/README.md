# Generated API docs

Plain-text `go doc -all` output for each public package, regenerate with:

```sh
go doc -all ./components > docs/components.txt
go doc -all ./devreload > docs/devreload.txt
go doc -all ./devwatch > docs/devwatch.txt
```

The API dumps of `xango` and `domrt` live in the xango repository,
[github.com/elaous/xango](https://github.com/elaous/xango).

Hand-written notes:

- `xango-canvas.md` -- how xango (function props, generic components, a
  possible `canvas/xango` renderer) can drive the canvas components.
- `deploy.md` -- deploying a wasm app: precompressed `.gz`/`.br`, the `application/wasm` type,
  cache headers, and nginx, Caddy and Go server settings.
- `e2e-helpers.md` -- how another module imports and runs the browser-test helpers `e2etest` and `screenreader`.
- `features.md` -- every feature of the editor with a picture and a few lines (the pictures come from OpenDocs, which regenerates them); `feature-testing.md` -- how to try each by hand and which test covers it.
