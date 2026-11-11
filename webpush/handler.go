package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

var ErrTooMany = errors.New("webpush: too many subscriptions")

// Config is what Handler needs.
type Config struct {
	VAPID     *VAPID
	Store     Store
	Authorize func(r *http.Request, handle string) bool
}

var handleRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

const maxBody = 8 << 10

func Handler(cfg Config) (http.Handler, error) {
	if cfg.VAPID == nil || cfg.Store == nil || cfg.Authorize == nil {
		return nil, errors.New("webpush: the handler needs a VAPID key, a store and an Authorize function")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /key", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"key": cfg.VAPID.Public()})
	})
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /subscribe", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Handle       string       `json:"handle"`
			Subscription Subscription `json:"subscription"`
		}
		if !read(w, r, &req) {
			return
		}
		if !handleRE.MatchString(req.Handle) || req.Subscription.Validate() != nil {
			http.Error(w, "invalid subscription", http.StatusBadRequest)
			return
		}
		if !cfg.Authorize(r, req.Handle) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		switch err := cfg.Store.Add(r.Context(), req.Handle, req.Subscription); {
		case errors.Is(err, ErrTooMany):
			http.Error(w, "too many subscriptions", http.StatusTooManyRequests)
		case err != nil:
			http.Error(w, "could not keep the subscription", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /unsubscribe", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Handle   string `json:"handle"`
			Endpoint string `json:"endpoint"`
		}
		if !read(w, r, &req) {
			return
		}
		if !handleRE.MatchString(req.Handle) || len(req.Endpoint) == 0 || len(req.Endpoint) > MaxEndpoint {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !cfg.Authorize(r, req.Handle) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		mine, err := cfg.Store.For(context.Background(), req.Handle)
		if err != nil {
			http.Error(w, "could not read the subscriptions", http.StatusInternalServerError)
			return
		}
		for _, e := range mine {
			if e.Subscription.Endpoint == req.Endpoint {
				_ = cfg.Store.Remove(r.Context(), req.Endpoint)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux, nil
}
