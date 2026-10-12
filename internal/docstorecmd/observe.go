package docstorecmd

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elaous/gojiberry/observe"
)

// shutdownGrace is how long a stop signal waits for requests to finish and the sinks to flush.
const shutdownGrace = 10 * time.Second

// setupObserver builds the operator observability the environment asks for (package observe,
// docs/observability.md). Unset or GOJIBERRY_OBS=off it returns nil and starts nothing: no sink is
// built, no secret file is read, no request leaves. An invalid setting is an error that names the
// variable. The Observer is also the process default, so that the sweeps report to it.
func setupObserver(ctx context.Context, getenv func(string) string) (*observe.Observer, error) {
	o, cfg, err := observe.FromEnv(ctx, getenv)
	for _, n := range cfg.Notes {
		log.Printf("docstore: WARNING: %s", n)
	}
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, nil
	}
	observe.SetDefault(o)
	log.Printf("docstore: operator observability is on (sinks: %v, usage counts also to: %v); GET /admin/observe shows what is sent (docs/observability.md)", cfg.Sinks, cfg.UsageSinks)
	return o, nil
}

// serve runs hs until it fails or a stop signal arrives. On a signal it stops accepting requests,
// lets the running ones finish, and flushes and closes the observer (so the last counts are sent).
func serve(hs *http.Server, o *observe.Observer) error {
	if o == nil {
		return hs.ListenAndServe()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, ok := <-sig; !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = hs.Shutdown(ctx)
		if err := o.Close(ctx); err != nil {
			log.Printf("docstore: closing the observability sinks: %v", err)
		}
	}()
	err := hs.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
