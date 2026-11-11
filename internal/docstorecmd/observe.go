package docstorecmd

import (
	"context"
	"net/http"

	"github.com/elaous/gojiberry/observe"
)

func setupObserver(ctx context.Context, getenv func(string) string) (*observe.Observer, error) {
	return observe.Setup(ctx, getenv, "docstore")
}

func serve(hs *http.Server, o *observe.Observer) error { return observe.Serve(hs, o) }
