package server

import (
	"net/netip"
	"testing"
)

func TestPickIP(t *testing.T) {
	ip, err := pickIP(map[string]netip.Addr{
		"raptor_b":  netip.MustParseAddr("172.18.0.5"),
		"raptor_a":  netip.MustParseAddr("172.19.0.7"),
		"raptor_v6": netip.MustParseAddr("fd00::5"),
	})
	if err != nil || ip != "172.19.0.7" {
		t.Errorf("%q, %v", ip, err)
	}
	if _, err := pickIP(nil); err == nil {
		t.Error("no networks: an address")
	}
}
