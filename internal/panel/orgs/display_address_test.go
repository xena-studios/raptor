package orgs

import "testing"

func TestDisplayAddress(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"  Play.Example.com ":    "play.example.com",
		"play.example.com:25565": "play.example.com:25565",
		"203.0.113.5":            "203.0.113.5",
		"203.0.113.5:27015":      "203.0.113.5:27015",
		"[2001:db8::1]:25565":    "[2001:db8::1]:25565",
		"2001:db8::1":            "2001:db8::1",
	} {
		if got, err := displayAddress(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"localhost", "play.example.com:0", "play.example.com:99999", "-bad.example.com", "a b.com", "https://play.example.com", "play.example.com/x", "[2001:db8::1]", "<script>.com"} {
		if _, err := displayAddress(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
