package docstorecmd

import (
	"context"
	"net/http"

	"github.com/elaous/gojiberry/observe"
)

// setupObserver builds the operator observability the environment asks for (package observe,
// docs/observability.md); nil when it is off.
func setupObserver(ctx context.Context, getenv func(string) string) (*observe.Observer, error) {
	return observe.Setup(ctx, getenv, "docstore")
}

// serve runs hs until it fails or a stop signal arrives, then flushes and closes the observer.
func serve(hs *http.Server, o *observe.Observer) error { return observe.Serve(hs, o) }
