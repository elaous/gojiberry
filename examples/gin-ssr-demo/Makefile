# gin-ssr-demo dev workflow. Separate Go module (like ../canvas-app):
# replace-ing github.com/elaous/gojiberry with the local checkout so it
# can depend on Gin and its security-middleware ecosystem without
# adding any of that to the root module.
.PHONY: run
run: generate
	@echo "open http://localhost:8097/?name=world"
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/gin-ssr-demo

.PHONY: test
test: generate
	go test ./...
