package main

import (
	"flag"
	"slices"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	for _, args := range [][]string{
		{"survival", "-f", "-n", "5"},
		{"-f", "survival", "-n", "5"},
		{"-n", "5", "-f", "survival"},
	} {
		fs := flag.NewFlagSet("logs", flag.ContinueOnError)
		f := fs.Bool("f", false, "")
		n := fs.Int("n", 100, "")
		pos, err := parseArgs(fs, args)
		if err != nil || !*f || *n != 5 || !slices.Equal(pos, []string{"survival"}) {
			t.Errorf("%q: pos=%q f=%v n=%d err=%v", args, pos, *f, *n, err)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:             "45s",
		12 * time.Minute:             "12m",
		3*time.Hour + 12*time.Minute: "3h12m",
		5*24*time.Hour + 3*time.Hour: "5d3h",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}
