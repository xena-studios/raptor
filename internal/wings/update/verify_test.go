package update

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"aead.dev/minisign"
)

func TestReleaseKeyMatchesRepo(t *testing.T) {
	data, err := os.ReadFile("../../../release/minisign.pub")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != ReleaseKey {
		t.Fatalf("ReleaseKey differs from release/minisign.pub:\n%s", data)
	}
	if _, err := ParseKey(ReleaseKey); err != nil {
		t.Fatal(err)
	}
}

// testdata holds the checksums of v0.0.1-rc.1 as published, signed with the
// release key by scripts/sign-release.sh.
func TestChecksumsRealRelease(t *testing.T) {
	key, _ := ParseKey(ReleaseKey)
	sums, _ := os.ReadFile("testdata/checksums.txt")
	sig, _ := os.ReadFile("testdata/checksums.txt.minisig")

	got, err := Checksums(key, "v0.0.1-rc.1", sums, sig)
	if err != nil {
		t.Fatal(err)
	}
	want := "c140bc6b57ff1609dce530756701afd9bf803f18cdebabc4a5e776da70074d74"
	if s := got["raptor_linux_arm64"]; hex.EncodeToString(s[:]) != want || len(got) != 2 {
		t.Fatalf("got %x", got)
	}

	// Another release's name: a replayed signature.
	if _, err := Checksums(key, "v0.0.2", sums, sig); err == nil || !strings.Contains(err.Error(), "signed for") {
		t.Fatalf("other tag: %v", err)
	}
	// One changed byte.
	bad := bytes.Replace(sums, []byte("c140"), []byte("c141"), 1)
	if _, err := Checksums(key, "v0.0.1-rc.1", bad, sig); err == nil {
		t.Fatal("tampered checksums verified")
	}
	// Another key.
	other, _, _ := minisign.GenerateKey(rand.Reader)
	if _, err := Checksums(other, "v0.0.1-rc.1", sums, sig); err == nil {
		t.Fatal("verified with another key")
	}
	// A changed trusted comment breaks the global signature.
	forged := bytes.Replace(sig, []byte("v0.0.1-rc.1"), []byte("v9.9.9"), 1)
	if _, err := Checksums(key, "v9.9.9", sums, forged); err == nil {
		t.Fatal("forged trusted comment verified")
	}
}

func TestChecksumsBadLines(t *testing.T) {
	pub, priv, _ := minisign.GenerateKey(rand.Reader)
	for _, body := range []string{
		"abc  raptor_linux_amd64\n",
		strings.Repeat("a", 64) + " raptor_linux_amd64\n", // one space
		strings.Repeat("zz", 32) + "  raptor_linux_amd64\n",
		strings.Repeat("a", 66) + "  raptor_linux_amd64\n",
		strings.Repeat("a", 64) + "  \n",
	} {
		sig := minisign.SignWithComments(priv, []byte(body), "raptor v1.0.0 checksums.txt", "")
		if _, err := Checksums(pub, "v1.0.0", []byte(body), sig); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func FuzzChecksums(f *testing.F) {
	pub, priv, _ := minisign.GenerateKey(rand.Reader)
	sums, _ := os.ReadFile("testdata/checksums.txt")
	f.Add(sums)
	f.Fuzz(func(t *testing.T, body []byte) {
		sig := minisign.SignWithComments(priv, body, "raptor v1.0.0 checksums.txt", "")
		_, _ = Checksums(pub, "v1.0.0", body, sig)
	})
}
