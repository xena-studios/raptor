package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// fakeResend answers POST /emails with the given statuses in turn and
// records what it was sent.
type fakeResend struct {
	mu       sync.Mutex
	statuses []int
	bodies   []map[string]any
	keys     []string
	auth     []string
}

func (f *fakeResend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies = append(f.bodies, body)
	f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	status := http.StatusOK
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
	if status == http.StatusOK {
		_, _ = w.Write([]byte(`{"id":"email_1"}`))
		return
	}
	_, _ = w.Write([]byte(`{"name":"error","message":"nope"}`))
}

func newFakeResend(t *testing.T, statuses ...int) (*Resend, *fakeResend) {
	t.Helper()
	f := &fakeResend{statuses: statuses}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	r := NewResend("re_test", "Raptor <no-reply@mail.example.test>")
	r.Client.BaseURL, _ = url.Parse(srv.URL + "/")
	return r, f
}

func TestResend(t *testing.T) {
	ctx := context.Background()
	r, f := newFakeResend(t)
	if err := r.Send(ctx, "alice@example.com", "Your code", "123456"); err != nil {
		t.Fatal(err)
	}
	b := f.bodies[0]
	if b["from"] != "Raptor <no-reply@mail.example.test>" || b["subject"] != "Your code" || b["text"] != "123456" {
		t.Errorf("body: %v", b)
	}
	if to, _ := b["to"].([]any); len(to) != 1 || to[0] != "alice@example.com" {
		t.Errorf("to: %v", b["to"])
	}
	if f.auth[0] != "Bearer re_test" || f.keys[0] == "" {
		t.Errorf("headers: auth %q, idempotency key %q", f.auth[0], f.keys[0])
	}

	// A rate limit is retried once, with the same idempotency key.
	r, f = newFakeResend(t, http.StatusTooManyRequests)
	if err := r.Send(ctx, "alice@example.com", "s", "t"); err != nil {
		t.Fatal(err)
	}
	if len(f.keys) != 2 || f.keys[0] != f.keys[1] {
		t.Errorf("retry keys: %v", f.keys)
	}
	// Twice is an error.
	r, _ = newFakeResend(t, http.StatusTooManyRequests, http.StatusTooManyRequests)
	if err := r.Send(ctx, "alice@example.com", "s", "t"); err == nil {
		t.Error("rate limited twice and no error")
	}
	// A refused request isn't retried.
	r, f = newFakeResend(t, http.StatusUnprocessableEntity)
	if err := r.Send(ctx, "alice@example.com", "s", "t"); err == nil || len(f.bodies) != 1 {
		t.Errorf("refused: %v after %d tries", err, len(f.bodies))
	}
	// Each email gets its own key.
	r, f = newFakeResend(t)
	_ = r.Send(ctx, "a@example.com", "s", "t")
	_ = r.Send(ctx, "a@example.com", "s", "t")
	if f.keys[0] == f.keys[1] {
		t.Error("two emails shared an idempotency key")
	}
}
