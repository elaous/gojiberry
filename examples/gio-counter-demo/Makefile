# gio-counter-demo dev workflow. Separate Go module (like
# ../canvas-app): replace-ing github.com/elaous/gojiberry with the
# local checkout so it can depend on gioui.org without adding it to
# the root module.
.PHONY: run
run: generate
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/gio-counter-demo

# test runs headless -- no display required (see goxgio's own doc
# comment on the real Gio GPU backend it exercises). run needs a real
# display; it's not part of test.
.PHONY: test
test: generate
	go test ./...
