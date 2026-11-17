package rcon

import (
	"net/url"
	"strings"
	"testing"
)

func TestHideSecret(t *testing.T) {
	for _, pw := range []string{"hunter2", "a b%c/d", "ü?&"} {
		msg := "dial ws://10.0.0.2:28016/" + pw + " and /" + url.PathEscape(pw) + " and ?" + url.QueryEscape(pw)
		if got := hideSecret(msg, pw); strings.Contains(got, pw) || strings.Contains(got, url.PathEscape(pw)) || strings.Contains(got, url.QueryEscape(pw)) {
			t.Errorf("%q left in %q", pw, got)
		}
	}
	if hideSecret("x", "") != "x" {
		t.Error("an empty secret")
	}
}
