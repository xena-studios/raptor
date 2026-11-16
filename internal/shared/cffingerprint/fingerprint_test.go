package cffingerprint

import "testing"

func TestOf(t *testing.T) {
	// MurmurHash2 with seed 1, checked against CurseForge's tooling.
	for _, c := range []struct {
		in   string
		want uint32
	}{
		{"", 0x5bd15e36},
		{"a b\tc\r\nd", Of([]byte("abcd"))},
	} {
		if got := Of([]byte(c.in)); got != c.want {
			t.Errorf("Of(%q) = %#x, want %#x", c.in, got, c.want)
		}
	}
}

// murmur2 is the textbook MurmurHash2 of b, whole, for checking the
// streaming one.
func murmur2(b []byte, seed uint32) uint32 {
	h := seed ^ uint32(len(b))
	i := 0
	for ; len(b)-i >= 4; i += 4 {
		k := uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24
		k *= m
		k ^= k >> 24
		k *= m
		h *= m
		h ^= k
	}
	switch len(b) - i {
	case 3:
		h ^= uint32(b[i+2]) << 16
		fallthrough
	case 2:
		h ^= uint32(b[i+1]) << 8
		fallthrough
	case 1:
		h ^= uint32(b[i])
		h *= m
	}
	h ^= h >> 13
	h *= m
	h ^= h >> 15
	return h
}

func TestStreaming(t *testing.T) {
	data := []byte("The quick brown fox\njumps over\tthe lazy dog. 0123456789!")
	for n := range len(data) {
		var clean []byte
		for _, b := range data[:n] {
			if !skip(b) {
				clean = append(clean, b)
			}
		}
		h := New(uint32(len(clean)))
		// Written in uneven pieces.
		for i := 0; i < n; i += 3 {
			_, _ = h.Write(data[i:min(i+3, n)])
		}
		if got, want := h.Sum32(), murmur2(clean, 1); got != want {
			t.Fatalf("n=%d: %#x, want %#x", n, got, want)
		}
	}
}
