package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

// Two migrations with one number break every start (goose refuses them),
// which parallel branches can do without a merge conflict.
func TestNumbersAreUnique(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, n := range names {
		num, _, ok := strings.Cut(n, "_")
		if !ok {
			t.Errorf("%s: no number", n)
			continue
		}
		if other, dup := seen[num]; dup {
			t.Errorf("%s and %s have the same number", other, n)
		}
		seen[num] = n
	}
}
