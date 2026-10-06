package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetLink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	orig := "# the dev VM\nlog:\n  level: debug # loud\npanel:\n  app_url: https://app.example.net\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetLink(p, "0192f0a4-0000-7000-8000-000000000001", "https://api.example.net"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeID != "0192f0a4-0000-7000-8000-000000000001" || cfg.Panel.URL != "https://api.example.net" ||
		cfg.Panel.AppURL != "https://app.example.net" || cfg.Log.Level != "debug" {
		t.Errorf("config: %+v", cfg)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# the dev VM") || !strings.Contains(string(b), "# loud") {
		t.Errorf("comments lost:\n%s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %s", fi.Mode().Perm())
	}
	// Unlinking removes the node ID.
	if err := SetLink(p, "", ""); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(p); cfg.NodeID != "" || cfg.Panel.URL != "https://api.example.net" {
		t.Errorf("after unlink: %+v", cfg)
	}
	// A missing file is created.
	p2 := filepath.Join(t.TempDir(), "new.yml")
	if err := SetLink(p2, "id-1", "https://api.example.net"); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(p2); err != nil || cfg.NodeID != "id-1" {
		t.Errorf("new file: %+v, %v", cfg, err)
	}
}

func TestSetQuotas(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(p, []byte("# mine\nlog:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetQuotas(p, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil || cfg.Storage.Quotas {
		t.Fatalf("quotas: %+v, %v", cfg.Storage, err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# mine") || !strings.Contains(string(b), "quotas: false") {
		t.Errorf("file:\n%s", b)
	}
}
