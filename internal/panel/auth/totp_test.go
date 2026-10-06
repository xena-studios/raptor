package auth

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
)

func TestSealTOTP(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s := &Service{DataKey: key}
	alice, bob := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	sealed, err := s.seal(alice, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("secret stored in the clear")
	}
	if got, err := s.open(alice, sealed); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q, %v", got, err)
	}
	// Copied to another user's row, it doesn't decrypt.
	if _, err := s.open(bob, sealed); err == nil {
		t.Error("opened under another user")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.open(alice, sealed); err == nil {
		t.Error("tampered secret opened")
	}
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	sealed[len(sealed)-1] ^= 1
	if _, err := (&Service{DataKey: other}).open(alice, sealed); err == nil {
		t.Error("opened with another key")
	}
	if _, err := (&Service{}).seal(alice, "x"); err == nil {
		t.Error("sealed without a key")
	}
}

func TestMatchTOTP(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	now := time.Unix(1_800_000_000, 0)
	step := now.Unix() / totpPeriod
	code := func(at time.Time) string {
		c, err := totp.GenerateCode(secret, at)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if st, ok := matchTOTP(secret, code(now), now, 0); !ok || st != step {
		t.Errorf("current code: %d, %v", st, ok)
	}
	// One step either way, for clocks that drift.
	if st, ok := matchTOTP(secret, code(now.Add(-30*time.Second)), now, 0); !ok || st != step-1 {
		t.Errorf("previous code: %d, %v", st, ok)
	}
	if _, ok := matchTOTP(secret, code(now.Add(30*time.Second)), now, 0); !ok {
		t.Error("next code refused")
	}
	if _, ok := matchTOTP(secret, code(now.Add(-90*time.Second)), now, 0); ok {
		t.Error("old code accepted")
	}
	// A used step can't be used again, nor anything before it.
	if _, ok := matchTOTP(secret, code(now), now, step); ok {
		t.Error("replayed code accepted")
	}
	if _, ok := matchTOTP(secret, code(now.Add(-30*time.Second)), now, step); ok {
		t.Error("code from before the last one accepted")
	}
	c := code(now)
	if _, ok := matchTOTP(secret, c[:3]+" "+c[3:], now, 0); !ok {
		t.Error("code with a space refused")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := matchTOTP(secret, bad, now, 0); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
