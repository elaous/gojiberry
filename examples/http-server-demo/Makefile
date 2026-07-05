# http-server-demo dev workflow: gox.Render served over real net/http,
# no WASM step.
.PHONY: run
run: generate
	@echo "open http://localhost:8089/?name=World"
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/http-server-demo

.PHONY: test
test: generate
	cd ../.. && go test ./examples/http-server-demo/...
