package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHealthz(t *testing.T) {
	draining := new(atomic.Bool)
	srv := httptest.NewServer(Handler(Config{Draining: draining}))
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
	// Shutting down: the proxy must stop sending it anything new.
	draining.Store(true)
	if s := status(); s != http.StatusServiceUnavailable {
		t.Fatalf("draining: status = %d, want 503", s)
	}
}
