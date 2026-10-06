package auth

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogMailerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.txt")
	m := LogMailer{Log: slog.New(slog.DiscardHandler), File: path}
	for _, to := range []string{"a@example.com", "b@example.com"} {
		if err := m.Send(context.Background(), to, "Your code: 123456", "code is 123456"); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Count(s, "Subject: Your code: 123456") != 2 || !strings.Contains(s, "To: b@example.com") {
		t.Errorf("file: %q", s)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v: codes in it sign people in", fi.Mode().Perm())
	}
}
