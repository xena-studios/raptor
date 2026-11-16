package mclogs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"Steve[/203.0.113.7:51234] logged in":                    "Steve[/[hidden]:51234] logged in",
		"Starting server on 0.0.0.0:25565":                       "Starting server on 0.0.0.0:25565",
		"bound to 127.0.0.1 and ::1":                             "bound to 127.0.0.1 and ::1",
		"from 2001:db8::5 via 3f9a2c71b0de.raptornodes.net":      "from [hidden] via [hidden]",
		"[12:34:56] [Server thread/INFO]: Done (3.2s)!":          "[12:34:56] [Server thread/INFO]: Done (3.2s)!",
		"Loading Paper 1.21.1 build 132, plugin 5.4.2360.0":      "Loading Paper 1.21.1 build 132, plugin 5.4.2360.0",
		"fe80::1ff:fe23:4567:890a%eth0 and 999.1.1.1 stay as is": "[hidden]%eth0 and 999.1.1.1 stay as is",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestShare(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/1/log" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		got = r.FormValue("content")
		switch got {
		case "evil":
			_, _ = w.Write([]byte(`{"success":true,"url":"https://evil.example/x"}`))
		case "":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"error":"Required POST argument 'content' is empty."}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"id":"abc","url":"https://mclo.gs/abc"}`))
		}
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, UserAgent: "test"}
	u, err := c.Share(context.Background(), "a log")
	if err != nil || u != "https://mclo.gs/abc" || got != "a log" {
		t.Fatalf("Share = %q, %v (sent %q)", u, err, got)
	}
	if _, err := c.Share(context.Background(), "evil"); err != ErrUnavailable {
		t.Errorf("a link elsewhere: %v", err)
	}
	if _, err := c.Share(context.Background(), ""); err == nil || err == ErrUnavailable {
		t.Errorf("mclo.gs' own error: %v", err)
	}
}
