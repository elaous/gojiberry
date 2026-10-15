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
   own before anything else is merged. Then run `make coverage-badge` and commit the README.md it rewrites (the badge is updated by hand: the
   hosted workflow is manual and no longer commits it), report the coverage score, and the count starts again from zero.
4. The merge count since the last full run is kept at the top of
   [roadmap.md](roadmap.md#merges-since-the-last-full-test-run), updated with each merge.

## Running the CI steps locally

No hosted runner (or no minutes) is needed: `make ci-local` runs the `make` steps of the workflow on this
machine, in the workflow's own order (`generate`, `fmt-check`, `vet`, `test`, `wasm-vet`, `lint`,
`wasm-lint`, `lint-modules`, `modules`, `wasm-size`, `a11y-test`, `grammar-test-setup`, `grammar-test`,
`mail-test`; each once). It reads them from every job of `.github/workflows/ci.yml`
(`.github/scripts/local-ci.sh`), so a step added there runs here too. The jobs that need a database
service (PostgreSQL, MongoDB) are not `make` steps and are not run locally.

The hosted workflow is manual ("Run workflow" on the Actions tab) and asks **what to run**: `all`,
`fast-checks`, `a11y` (the accessibility gate in the runner's Chrome), `mailpit` (the SMTP sender against
the `axllent/mailpit` Docker image), `grammar`, `postgres` or `mongodb`.

- `make ci-local` stops at the first failure; `.github/scripts/local-ci.sh -k` keeps going and lists
  every failure at the end.
- `make ci-local STEPS="wasm-size a11y-test"` runs only those steps.
- `LOCAL_CI_SKIP` is the steps to leave out; the default is `coverage-badge` (it runs the whole test suite a second time, and the
  badge is updated by hand; run `make coverage-badge` yourself at a full run and commit README.md).
  `LOCAL_CI_LIST=1` prints the steps and stops.
- `local-ci-report.txt` (ignored by git) has the commit, and each step with its time and result.
- `a11y-test` needs a Chromium; set `CHROMIUM` if it is not on the PATH. The manual screen-reader run
  of [accessibility-testing.md](accessibility-testing.md) is still done by a person.

## Mailpit-backed tests (SMTP)

The SMTP sender in `mail` is tested against real [Mailpit](https://mailpit.axllent.org) servers: the
tests send through the sender and read the result back through Mailpit's HTTP API (subject with accents,
recipients, a long line, a line that is only a dot, the HTML alternative, wrong and missing passwords,
no downgrade). They skip without `GOJIBERRY_TEST_MAILPIT_SMTP` and `GOJIBERRY_TEST_MAILPIT_API`, so a plain
`go test ./...` does not need Mailpit.

`make mail-test` starts two Mailpits and runs the tests against each: STARTTLS with a self-signed
certificate and a required sign-in, and plain on loopback. It uses the image `axllent/mailpit` when a
Docker daemon is running, otherwise a `mailpit` binary on the PATH, otherwise builds
`github.com/axllent/mailpit` with `go install` (`MAILPIT=docker|binary` forces one). It fails if a test
is skipped. To try by hand:

```
docker run -d --name mailpit -p 1025:1025 -p 8025:8025 axllent/mailpit
GOJIBERRY_TEST_MAILPIT_SMTP=127.0.0.1:1025 GOJIBERRY_TEST_MAILPIT_API=http://127.0.0.1:8025 go test -run Mailpit -v ./mail
```

and open http://127.0.0.1:8025 to see the messages. (The `mail-mailpit` job of the CI workflow runs
`make mail-test`.) The optional variables `GOJIBERRY_TEST_MAILPIT_CA`, `…_USER` and `…_PASS` are the
server's certificate file and sign-in when it has them.

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

## PDF/UA check

`internal/officecheck` converts a small corpus (HTML sources in `testdata/corpus`) with LibreOffice and reads the DOCX and ODT back with the importers, and opens the DOCX, ODT and RTF the exporters write in LibreOffice (needs `soffice` with Writer; `GOJIBERRY_SOFFICE`, `GOJIBERRY_REQUIRE_SOFFICE=1`; skipped otherwise). `TestVeraPDFAcceptsTheTaggedFiles` runs veraPDF (PDF/UA-1) on three exported PDFs when `verapdf` is installed (Java; `GOJIBERRY_VERAPDF=/path/to/verapdf`); `GOJIBERRY_REQUIRE_VERAPDF=1` fails it when it is missing, otherwise it is skipped. `make pdfa11y-test` installs pdfa11y (a Go PDF/UA checker, pinned) into `.pdfa11y/` and runs it on the PDFs the exporter writes; the test fails if the tool is missing. Without the make target the test is skipped with "SKIPPED, NOT TESTED" in its log. Run it when `export/pdf*.go` changes.
