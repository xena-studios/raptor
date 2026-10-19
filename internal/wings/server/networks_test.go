package server

import (
	"slices"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Survival SMP":          "survival-smp",
		"  --Lobby #1-- ":       "lobby-1",
		"Créative":              "cr-ative",
		"!!!":                   "",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slug(strings.Repeat("a", 62) + " b"); len(got) > 63 || strings.HasSuffix(got, "-") {
		t.Errorf("Slug ends in a hyphen or is too long: %q", got)
	}
}

func TestAliases(t *testing.T) {
	got := Aliases("0199a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b", "Survival")
	if want := []string{"survival.raptor", "1c2d3e4f5a6b.raptor"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidateNetworks(t *testing.T) {
	if err := validateNetworks([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"a", "a"}, {""}, slices.Repeat([]string{"x"}, MaxNetworks+1)} {
		if err := validateNetworks(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
