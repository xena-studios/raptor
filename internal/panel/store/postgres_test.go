package store

import "testing"

func TestPoolConfig(t *testing.T) {
	for _, c := range []struct {
		url  string
		want int32
	}{
		{"postgres://u:p@localhost/db", minConns},
		{"postgres://u:p@localhost/db?sslmode=disable", minConns},
		{"postgres://u:p@localhost/db?pool_max_conns=40", 40},
		{"postgres://u:p@localhost/db?pool_max_conns=2", 2},
	} {
		cfg, err := poolConfig(c.url)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxConns != c.want {
			t.Errorf("%s: MaxConns %d, want %d", c.url, cfg.MaxConns, c.want)
		}
	}
}
