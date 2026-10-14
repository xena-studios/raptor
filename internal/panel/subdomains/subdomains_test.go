package subdomains

import (
	"errors"
	"testing"
)

func TestCheckName(t *testing.T) {
	for name, want := range map[string]error{
		"smp": nil, "my-server-2": nil, "abc": nil,
		"ab": ErrName, "-smp": ErrName, "smp-": ErrName, "S.M.P": ErrName, "xn--pple": ErrName, "a--b": ErrName,
		"thirty-three-characters-long-xyz1": ErrName,
		"www":                               ErrReserved, "api": ErrReserved, "n-abc12345": ErrReserved,
	} {
		if got := CheckName(name); !errors.Is(got, want) || (want == nil && got != nil) {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}

func TestSRVPort(t *testing.T) {
	for cfg, want := range map[string]int{
		`{"allocations":[{"port":25565},{"port":25570,"primary":true}],"egg":{"players":"minecraft"}}`: 25570,
		`{"allocations":[{"port":25565}],"egg":{"players":"minecraft"}}`:                               25565,
		`{"allocations":[{"port":28015,"primary":true}],"egg":{"players":"source"}}`:                   0,
		`{"allocations":[{"port":25565,"primary":true}]}`:                                              0,
		`not json`: 0,
	} {
		if got := srvPort([]byte(cfg)); got != want {
			t.Errorf("%s: %d, want %d", cfg, got, want)
		}
	}
}
