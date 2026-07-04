# gox-canvas dev workflow. Run `make run` for the full path from a
# clean checkout: starts Postgres, applies migrations via Atlas,
# regenerates .go from .gox, builds the WASM autosave client, and runs
# the dev server.
#
# DATABASE_URL matches docker-compose.yml's mapped host port (5433,
# not 5432 -- see that file's comment on why).
DATABASE_URL := postgres://canvas:canvas@127.0.0.1:5433/canvas?sslmode=disable

.PHONY: run
run: generate wasm db-up db-migrate
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/devserver

# watch is `run`, but rebuilding and restarting the dev server on every
# source change instead of running it once (see cmd/devwatch). The
# browser tab auto-reloads once the new process is back up.
.PHONY: watch
watch: db-up db-migrate
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/devwatch

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/canvas-app

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o static/main.wasm ./cmd/wasmclient/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" static/

.PHONY: db-up
db-up:
	docker compose up -d --wait

.PHONY: db-down
db-down:
	docker compose down

## atlas.hcl's "local" env carries the golang-migrate directory format
## Encore also requires (<number>_<description>.up.sql) -- it must be
## picked up via --env, which only resolves atlas.hcl relative to the
## working directory, hence `cd canvas` in every atlas invocation below.

.PHONY: db-migrate
db-migrate:
	cd canvas && atlas migrate apply --env local --url "$(DATABASE_URL)"

# db-diff regenerates a migration from canvas/schema.sql after you've
# edited it, by diffing against an ephemeral Atlas-managed dev
# database (not the one docker-compose runs) -- e.g.
# `make db-diff name=add_title_column`.
.PHONY: db-diff
db-diff:
	cd canvas && atlas migrate diff --env local $(name)

.PHONY: db-status
db-status:
	cd canvas && atlas migrate status --env local --url "$(DATABASE_URL)"

.PHONY: test
test: generate
	DATABASE_URL="$(DATABASE_URL)" go test ./...

.PHONY: clean
clean:
	docker compose down -v
	rm -f static/main.wasm static/wasm_exec.js
