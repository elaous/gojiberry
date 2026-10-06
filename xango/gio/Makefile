# xango/gio dev workflow. Separate Go module (like ../../examples/gio-
# counter-demo, which now depends on this one instead of defining it
# locally): replace-ing github.com/elaous/gojiberry with the local
# checkout so it can depend on gioui.org without adding it to the root
# module.
.PHONY: test
test:
	go test ./...
