// Package cffingerprint computes CurseForge's file fingerprint: MurmurHash2
// (seed 1) of the file without its whitespace bytes (tab, newline,
// carriage return, space), which is what CurseForge looks files up by.
package cffingerprint

const m = 0x5bd1e995

func skip(b byte) bool { return b == 9 || b == 10 || b == 13 || b == 32 }

// Hash computes a fingerprint as bytes are written, one pass and no
// buffering of the file: the length goes into the seed, so it's counted
// first by Len, then the bytes are written.
type Hash struct {
	h     uint32
	tail  [4]byte
	n     int
	ready bool
}

// New starts a fingerprint of a file with length non-whitespace bytes.
func New(length uint32) *Hash { return &Hash{h: 1 ^ length, ready: true} }

// Write adds bytes (whitespace is skipped).
func (f *Hash) Write(p []byte) (int, error) {
	for _, b := range p {
		if skip(b) {
			continue
		}
		f.tail[f.n] = b
		f.n++
		if f.n == 4 {
			k := uint32(f.tail[0]) | uint32(f.tail[1])<<8 | uint32(f.tail[2])<<16 | uint32(f.tail[3])<<24
			k *= m
			k ^= k >> 24
			k *= m
			f.h = f.h*m ^ k
			f.n = 0
		}
	}
	return len(p), nil
}

// Sum32 finishes the fingerprint.
func (f *Hash) Sum32() uint32 {
	h := f.h
	switch f.n {
	case 3:
		h ^= uint32(f.tail[2]) << 16
		fallthrough
	case 2:
		h ^= uint32(f.tail[1]) << 8
		fallthrough
	case 1:
		h ^= uint32(f.tail[0])
		h *= m
	}
	h ^= h >> 13
	h *= m
	h ^= h >> 15
	return h
}

// Counter counts the bytes a fingerprint covers, as they're written.
type Counter struct{ N uint32 }

// Write counts p's non-whitespace bytes.
func (c *Counter) Write(p []byte) (int, error) {
	for _, b := range p {
		if !skip(b) {
			c.N++
		}
	}
	return len(p), nil
}

// Of is the fingerprint of b.
func Of(b []byte) uint32 {
	var n uint32
	for _, c := range b {
		if !skip(c) {
			n++
		}
	}
	h := New(n)
	_, _ = h.Write(b)
	return h.Sum32()
}
