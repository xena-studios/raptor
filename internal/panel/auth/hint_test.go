package auth

import (
	"net/http"
	"strings"
	"testing"
)

// The hint cookie is set for the site's domain, holds no token, is
// readable by the site's scripts, and is cleared at sign-out; with no
// domain configured, nothing is set.
func TestSetHint(t *testing.T) {
	off := &Service{}
	h := http.Header{}
	off.setHint(h, true)
	if len(h.Values("Set-Cookie")) != 0 {
		t.Fatalf("no HintDomain: want no cookie, got %q", h.Values("Set-Cookie"))
	}

	s := &Service{HintDomain: "raptorpanel.net"}
	h = http.Header{}
	s.setHint(h, true)
	set := h.Get("Set-Cookie")
	for _, want := range []string{"raptor_signed_in=1", "Domain=raptorpanel.net", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(set, want) {
			t.Errorf("set: %q lacks %q", set, want)
		}
	}
	if strings.Contains(set, "HttpOnly") {
		t.Errorf("set: %q is HttpOnly; the site's scripts must read it", set)
	}

	h = http.Header{}
	s.setHint(h, false)
	clear := h.Get("Set-Cookie")
	if !strings.Contains(clear, "raptor_signed_in=;") || !strings.Contains(clear, "Max-Age=0") {
		t.Errorf("clear: %q doesn't expire the cookie", clear)
	}
}
