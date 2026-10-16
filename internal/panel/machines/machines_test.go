package machines

import "testing"

func TestWant(t *testing.T) {
	for _, c := range []struct{ name, custom, want string }{
		{"Basement Box #2", "", "basement-box-2"},
		{"  game-box-1 ", "", "game-box-1"},
		{"Café Ünïcode", "", "caf-n-code"},
		{"!!!", "", "n-abcd2345"},
		{"Any", "my-host.lan", "my-host.lan"},
		{"a-very-long-name-that-goes-on-and-on-and-on-past-sixty-three-characters-x", "", "a-very-long-name-that-goes-on-and-on-and-on-past-sixty-three-ch"},
	} {
		if got := Want(c.name, "abcd2345", c.custom); got != c.want {
			t.Errorf("Want(%q, %q) = %q, want %q", c.name, c.custom, got, c.want)
		}
	}
}
