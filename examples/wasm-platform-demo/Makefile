# wasm-platform-demo dev workflow. Run `make run` for the full path from a
# clean checkout: regenerates .go from .gox, builds the WASM client,
# and serves it -- then open http://localhost:8080/.
.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/wasm-platform-demo

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: clean
clean:
	rm -f main.wasm wasm_exec.js
