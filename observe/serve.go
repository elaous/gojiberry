package observe

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ShutdownGrace is how long a stop signal waits for requests to finish and the sinks to flush.
const ShutdownGrace = 10 * time.Second

// Setup builds the operator observability the environment asks for, for a server of the project
// (service is the name the sinks report, and the prefix of the log lines). Unset or GOJIBERRY_OBS=off
// it returns nil and starts nothing: no sink is built, no secret file is read, nothing leaves. An
// invalid setting is an error that names the variable. The Observer is also the process default.
func Setup(ctx context.Context, getenv func(string) string, service string) (*Observer, error) {
	o, cfg, err := FromEnvFor(ctx, getenv, service)
	for _, n := range cfg.Notes {
		log.Printf("%s: WARNING: %s", service, n)
	}
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, nil
	}
	SetDefault(o)
	log.Printf("%s: operator observability is on (sinks: %v, usage counts also to: %v) (docs/observability.md)", service, cfg.Sinks, cfg.UsageSinks)
	if len(cfg.Sinks) == 1 && cfg.Sinks[0] == "builtin" && service != ServiceName {
		log.Printf("%s: WARNING: only the builtin sink is on, and this server has no /admin/observe page: name otlp or posthog in %s to see anything", service, envSinks)
	}
	return o, nil
}

// Serve runs hs until it fails or a stop signal arrives. On a signal it stops accepting requests, lets
// the running ones finish, and flushes and closes the observer (so the last counts are sent). With a nil
// observer it is hs.ListenAndServe.
func Serve(hs *http.Server, o *Observer) error {
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
		ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
		defer cancel()
		_ = hs.Shutdown(ctx)
		if err := o.Close(ctx); err != nil {
			log.Printf("closing the observability sinks: %v", err)
		}
	}()
	err := hs.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
