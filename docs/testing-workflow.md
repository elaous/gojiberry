# Testing workflow

CI (`.github/workflows/ci.yml`) is **manual** for now: run it from the Actions tab when a full
remote check is wanted. The checks run here instead.

1. **Before every push**, run the targeted checks for what changed: `gofmt -l .`, `go build ./...`,
   `go vet` and `GOOS=js GOARCH=wasm go vet` on the touched packages, and `go test` on those packages
   (`-race` for concurrency-sensitive ones such as `saver` and `docstore/encrypt`; a short browser
   test only when the change touches a page). Nested modules (`docstore/backends`, the Gio ones, the
   examples with their own `go.mod`) are checked when their files or their dependencies change
   (`make modules`).
2. **Merge by squash**, one PR at a time. A stack is merged bottom-up, retargeting each PR to `main`
   after the one below it merges.
3. **Every fifth merge in a row**, after squashing and merging the fifth PR, run the **full** suite on
   `main` (`make check`, or `go build ./... && go vet ./... && GOOS=js GOARCH=wasm go vet ./... &&
   go test ./...` plus `make modules`), and fix what it finds on a branch of its own before anything
   else is merged. Then the count starts again from zero.
4. The merge count since the last full run is kept at the top of
   [roadmap.md](roadmap.md#merges-since-the-last-full-test-run), updated with each merge.
