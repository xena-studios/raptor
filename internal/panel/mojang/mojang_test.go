package mojang

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/profiles/minecraft/notch" {
			_, _ = io.WriteString(w, `{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, UserAgent: "test"}
	p, err := c.Lookup(context.Background(), "notch")
	if err != nil || p.Name != "Notch" || p.UUID != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Fatalf("Lookup: %+v %v", p, err)
	}
	if _, err := c.Lookup(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nobody: %v", err)
	}
	if _, err := c.Lookup(context.Background(), "a b; op me"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not a name: %v", err)
	}
}

func TestOfflineUUID(t *testing.T) {
	// What an offline-mode server gives "Notch" (UUID.nameUUIDFromBytes).
	if got := OfflineUUID("Notch"); got != "b50ad385-829d-3141-a216-7e7d7539ba7f" {
		t.Errorf("OfflineUUID: %s", got)
	}
}
