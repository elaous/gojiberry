# showcase-app dev workflow. Run `make run` for the full path from a
# clean checkout: regenerates .go from every *.xg in this tree, builds
# the main WASM client and the separate worker/ WASM binary it spawns,
# stages the components package's CSS alongside them, and serves it --
# then open http://localhost:8080/.
.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/showcase-app

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	(cd worker && GOOS=js GOARCH=wasm go build -o ../worker.wasm .)
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .
	cp ../../components/*.css .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: clean
clean:
	rm -f main.wasm worker.wasm wasm_exec.js *.css
