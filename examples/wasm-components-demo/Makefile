# wasm-components-demo dev workflow. Run `make run` for the full path
# from a clean checkout: regenerates .go from .xg, builds the WASM
# client, stages the components package's CSS alongside it, and serves
# it -- then open http://localhost:8080/.
.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go tool xgc examples/wasm-components-demo

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .
	cp ../../components/*.css .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: screenshot
screenshot: generate
	OUT_FILE="$$(pwd)/screenshot.png" go test -count=1 -run TestScreenshot .
	@echo "wrote screenshot.png"

.PHONY: clean
clean:
	rm -f main.wasm wasm_exec.js *.css screenshot.png
