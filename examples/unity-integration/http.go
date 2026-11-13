package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ws", f.server.WebSocketHandler())
	mux.HandleFunc("GET /integration/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.snapshot())
	})
	mux.HandleFunc("POST /integration/start", func(w http.ResponseWriter, r *http.Request) {
		if err := f.start(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /integration/checkpoint", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := f.post(ctx, f.capture); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func (f *fixture) post(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	if err := f.server.Post(func(*golem.Server) { done <- fn() }); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
