package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHealthz(t *testing.T) {
	draining := new(atomic.Bool)
	var down atomic.Bool
	ready := func(context.Context) error {
		if down.Load() {
			return errors.New("database unreachable")
		}
		return nil
	}
	srv := httptest.NewServer(Handler(Config{Draining: draining, Ready: ready}))
	defer srv.Close()

	status := func() int {
		t.Helper()
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if s := status(); s != http.StatusOK {
		t.Fatalf("status = %d, want 200", s)
	}
	// The database gone: up as a process, but not serving.
	down.Store(true)
	if s := status(); s != http.StatusServiceUnavailable {
		t.Fatalf("database down: status = %d, want 503", s)
	}
	down.Store(false)
	// Shutting down: the proxy must stop sending it anything new.
	draining.Store(true)
	if s := status(); s != http.StatusServiceUnavailable {
		t.Fatalf("draining: status = %d, want 503", s)
	}
}
