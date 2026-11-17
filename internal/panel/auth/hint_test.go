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

// GetSession's sync sets the hint for a signed-in browser that lacks it,
// clears one left behind after the session ended, and leaves a correct
// cookie and the phone app alone.
func TestSyncHint(t *testing.T) {
	s := &Service{HintDomain: "raptorpanel.net"}
	cases := []struct {
		name     string
		cookie   string
		bearer   bool
		signedIn bool
		want     string
	}{
		{"signed in, no hint", "", false, true, "raptor_signed_in=1"},
		{"signed in, has hint", "raptor_signed_in=1", false, true, ""},
		{"signed out, stale hint", "raptor_signed_in=1", false, false, "raptor_signed_in=;"},
		{"signed out, no hint", "", false, false, ""},
		{"phone app", "", true, true, ""},
	}
	for _, tc := range cases {
		req, resp := http.Header{}, http.Header{}
		if tc.cookie != "" {
			req.Set("Cookie", tc.cookie)
		}
		if tc.bearer {
			req.Set("Authorization", "Bearer x")
		}
		s.syncHint(req, resp, tc.signedIn)
		got := resp.Get("Set-Cookie")
		if tc.want == "" && got != "" {
			t.Errorf("%s: want no cookie, got %q", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
