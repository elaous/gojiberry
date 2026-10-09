# Testing workflow

CI (`.github/workflows/ci.yml`) is **manual** for now: run it from the Actions tab when a full
remote check is wanted. The checks run here instead.

1. **Before every push**, run the targeted checks for what changed: `gofmt -l .`, `go build ./...`,
   `go vet` and `GOOS=js GOARCH=wasm go vet` on the touched packages, `make lint` and `make wasm-lint`
   (about two minutes each, whole root module), and `go test` on those packages
   (`-race` for concurrency-sensitive ones such as `saver` and `docstore/encrypt`; a short browser
   test only when the change touches a page). Nested modules (`docstore/backends`, the Gio ones, the
   examples with their own `go.mod`; the examples are linted like the library, there is no path exclusion) are checked when their files or their dependencies change
   (`make modules` and `make lint-modules`).
2. **Merge by squash**, one PR at a time. A stack is merged bottom-up, retargeting each PR to `main`
   after the one below it merges.
3. **Every tenth merge in a row**, after squashing and merging the tenth PR, run the **full** suite on
   `main` (`make check`, or `go build ./... && go vet ./... && GOOS=js GOARCH=wasm go vet ./... &&
   go test ./...` plus `make lint`, `make wasm-lint`, `make modules` and `make lint-modules`), and fix what it finds on a branch of its
   own before anything else is merged. Then the count starts again from zero.
4. The merge count since the last full run is kept at the top of
   [roadmap.md](roadmap.md#merges-since-the-last-full-test-run), updated with each merge.

## Postgres-backed tests

`examples/canvas-app/cmd/devserver` has two tests that run against a real PostgreSQL (`TestDevserverEndToEnd`
and `TestDevModeInjectsLiveReloadScript`). They **skip** when no server answers, and a plain `go test` still
prints `ok`, so a skip hides a stale test (this is how a wrong status expectation went unnoticed). The skip
message starts with `SKIPPED, NOT TESTED` and names the variable. Run them with `-v` and look for `SKIP`, or
start a server and set `TEST_DATABASE_URL` (default
`postgres://canvas:canvas@127.0.0.1:5432/canvas?sslmode=disable`). The database needs the `documents` table.

A throwaway instance, no Docker, from the repo root (Debian/Ubuntu paths; run `initdb` and `pg_ctl` as a
non-root user, for example `su postgres -c '...'` when you are root):

```
PGDATA=$(mktemp -d) && chown postgres "$PGDATA"        # drop the chown when not root
B=/usr/lib/postgresql/16/bin
su postgres -c "$B/initdb -D $PGDATA/data -A trust -U postgres"
su postgres -c "$B/pg_ctl -D $PGDATA/data -o '-p 5432 -k $PGDATA -c listen_addresses=127.0.0.1' -l $PGDATA/log -w start"
psql -h 127.0.0.1 -U postgres -c "create user canvas password 'canvas'" -c "create database canvas owner canvas"
PGPASSWORD=canvas psql -h 127.0.0.1 -U canvas canvas -f examples/canvas-app/canvas/migrations/1_create_documents.up.sql
(cd examples/canvas-app && go test -count=1 -v ./...)
su postgres -c "$B/pg_ctl -D $PGDATA/data -m fast stop" && rm -rf "$PGDATA"
```

With Docker, `make db-up db-migrate test` in `examples/canvas-app` starts the Compose Postgres (port 5433),
applies the migrations with Atlas and runs the tests (`make test` passes the Compose URL as `TEST_DATABASE_URL`;
it used to set only `DATABASE_URL`, which the tests ignore, so they skipped against port 5432). CI runs the same tests in the `canvas-app-postgres` job
(`.github/workflows/ci.yml`) against a Postgres service container and fails if they skip.

## MongoDB-backed tests

The tests in `docstore/backends/mongo` (conformance, `SaveIf`, `DeleteIf`, `ListPrefix`, the prefix filter and the
head-reader check) need a real MongoDB; the driver has nothing small enough to fake. They **skip** without
`TEST_MONGO_URI`, and a plain `go test` still prints `ok`, so each skip message starts with `SKIPPED, NOT TESTED`.
They passed against MongoDB 7.0.14 with `-race`; the first real run found that the conformance factory did not
empty its collection between subtests (a test setup bug, not an adapter bug), which the skip had been hiding.

With Docker:

```
docker run -d --name mongotest -p 27017:27017 mongo:7
(cd docstore/backends && TEST_MONGO_URI=mongodb://127.0.0.1:27017 go test -count=1 -race -v ./mongo/)
docker rm -f mongotest
```

Without Docker, a throwaway server from the official tarball (Linux x86-64; pick the build that matches your
distribution from the MongoDB download page):

```
D=$(mktemp -d)
curl -sS -o $D/m.tgz https://fastdl.mongodb.org/linux/mongodb-linux-x86_64-ubuntu2204-7.0.14.tgz
tar xzf $D/m.tgz -C $D && mkdir $D/data
$D/mongodb-linux-x86_64-ubuntu2204-7.0.14/bin/mongod --dbpath $D/data --port 27018 --bind_ip 127.0.0.1 \
    --fork --logpath $D/log.txt
(cd docstore/backends && TEST_MONGO_URI=mongodb://127.0.0.1:27018 go test -count=1 -race -v ./mongo/)
kill $(pgrep -f "mongod --dbpath $D/data"); rm -rf $D
```

## Other tests that skip without an external service or tool

None of these change behaviour; they only skip. Run each with `-v` and check that it did not skip.

| Test | Needs | How to run |
| --- | --- | --- |
| `docstore/backends/mongo`: `TestMongoAdapterConformance`, `TestMongoAdapterSaveIf` and four more | A MongoDB server, `TEST_MONGO_URI` (optional `TEST_MONGO_DB`, default `docstore_test`) | See "MongoDB-backed tests" above. CI runs them in the `mongo-backend` job and fails on a skip. |
| `internal/e2etest` browser tests (used by `examples/canvas-*-demo`, `domrt-bench` and others) | A Chromium or Chrome binary: `CHROMIUM` or `PLAYWRIGHT_CHROMIUM`, `/opt/pw-browsers/chromium`, or `chromium`/`google-chrome` on `PATH` | `CHROMIUM=/path/to/chromium go test -count=1 -v ./...` in the example's directory. |
| `examples/wasm-components-demo`: `TestScreenshot` | Opt-in, not a service: `OUT_FILE` and a Chromium | `make screenshot` in that directory (it sets `OUT_FILE`). |

Other `t.Skip` calls in the repo depend on the platform or a rare condition (`docstore/localfs_test.go` on
non-Linux, `internal/transpile/experimental` on Windows, `saver/pack_test.go`, `console/console_test.go`,
`devreload/devreload_test.go`), not on a service.
