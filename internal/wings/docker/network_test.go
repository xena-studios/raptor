package docker

import (
	"net/netip"
	"slices"
	"testing"
)

func TestHostRoutes(t *testing.T) {
	got, err := hostRoutes("testdata/route")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("172.17.0.0/16"),
		netip.MustParsePrefix("192.168.5.0/24"),
		netip.MustParsePrefix("172.30.0.0/16"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPickSubnet(t *testing.T) {
	cases := []struct {
		used []string
		want string
	}{
		{nil, "172.29.0.0/16"},
		{[]string{"172.29.5.0/24"}, "172.30.0.0/16"},
		// A VPN or provider route covering all of 172.16/12 pushes it to 10.x.
		{[]string{"172.16.0.0/12"}, "10.200.0.0/16"},
		{[]string{"172.16.0.0/12", "10.0.0.0/8"}, ""},
	}
	for _, c := range cases {
		var used []netip.Prefix
		for _, u := range c.used {
			used = append(used, netip.MustParsePrefix(u))
		}
		got, err := pickSubnet(used)
		if c.want == "" {
			if err == nil {
				t.Errorf("used %v: got %v, want an error", c.used, got)
			}
			continue
		}
		if err != nil || got.String() != c.want {
			t.Errorf("used %v: got %v (%v), want %s", c.used, got, err, c.want)
		}
	}
}
