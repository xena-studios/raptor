package backup

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func backupsAt(times ...time.Time) []candidate {
	out := make([]candidate, len(times))
	for i, t := range times {
		out[i] = candidate{ID: fmt.Sprint(i), At: t}
	}
	return out
}

func kept(r Retention, bs []candidate, loc *time.Location) []string {
	gone := r.expired(bs, loc)
	var out []string
	for _, b := range bs {
		if !slices.Contains(gone, b.ID) {
			out = append(out, b.ID)
		}
	}
	return out
}

var day0 = time.Date(2026, 3, 2, 4, 30, 0, 0, time.UTC) // a Monday

func TestRetentionRules(t *testing.T) {
	// Daily backups for 60 days, and an extra manual one on the last day.
	var times []time.Time
	for d := range 60 {
		times = append(times, day0.AddDate(0, 0, d))
	}
	times = append(times, day0.AddDate(0, 0, 59).Add(6*time.Hour))
	bs := backupsAt(times...)
	ids := func(idx ...int) []string {
		out := make([]string, len(idx))
		for i, x := range idx {
			out[i] = fmt.Sprint(x)
		}
		slices.Sort(out)
		return out
	}
	for _, tc := range []struct {
		name string
		r    Retention
		want []string
	}{
		{"last", Retention{KeepLast: 3}, ids(60, 59, 58)},
		// The newest of each day: the manual one wins day 59.
		{"daily", Retention{KeepDaily: 3}, ids(60, 58, 57)},
		// ISO weeks start on Monday; day 59 is a Tuesday (weeks: 56–59, 49–55).
		{"weekly", Retention{KeepWeekly: 2}, ids(60, 55)},
		// Months: April 30 (day 59), March 31 (day 29).
		{"monthly", Retention{KeepMonthly: 2}, ids(60, 29)},
		{"combined", Retention{KeepLast: 2, KeepDaily: 3, KeepWeekly: 2}, ids(60, 59, 58, 57, 55)},
	} {
		got := kept(tc.r, bs, time.UTC)
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: kept %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Days are counted in the node's time zone.
func TestRetentionTimezone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 03:00 and 05:00 UTC on the same UTC day are different New York days.
	bs := backupsAt(time.Date(2026, 3, 3, 3, 0, 0, 0, time.UTC), time.Date(2026, 3, 3, 5, 0, 0, 0, time.UTC))
	if got := kept(Retention{KeepDaily: 2}, bs, ny); len(got) != 2 {
		t.Fatalf("New York days: kept %v", got)
	}
	if got := kept(Retention{KeepDaily: 2}, bs, time.UTC); len(got) != 1 {
		t.Fatalf("UTC days: kept %v", got)
	}
}

// A server offline for months keeps its last backups: periods without a
// backup don't count.
func TestRetentionGaps(t *testing.T) {
	bs := backupsAt(day0, day0.AddDate(0, 0, 1), day0.AddDate(0, 0, 2), day0.AddDate(0, 6, 0))
	if got := kept(Retention{KeepDaily: 3}, bs, time.UTC); len(got) != 3 || slices.Contains(got, "0") {
		t.Fatalf("kept %v", got)
	}
}

// Properties over random histories: the newest backup is always kept, no
// more are kept than the rules allow, and retention never deletes
// everything.
func TestRetentionProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		r := Retention{KeepLast: rng.IntN(4), KeepDaily: rng.IntN(8), KeepWeekly: rng.IntN(5), KeepMonthly: rng.IntN(4)}
		if r == (Retention{}) {
			if got := r.expired(backupsAt(day0), time.UTC); got != nil {
				t.Fatalf("empty retention deleted %v", got)
			}
			continue
		}
		var times []time.Time
		for range rng.IntN(80) + 1 {
			times = append(times, day0.Add(time.Duration(rng.Int64N(int64(200*24*time.Hour)))))
		}
		bs := backupsAt(times...)
		k := kept(r, bs, time.UTC)
		newest := slices.MaxFunc(bs, func(a, b candidate) int { return a.At.Compare(b.At) })
		if !slices.Contains(k, newest.ID) {
			t.Fatalf("%+v dropped the newest backup", r)
		}
		if limit := r.KeepLast + r.KeepDaily + r.KeepWeekly + r.KeepMonthly; len(k) > limit {
			t.Fatalf("%+v kept %d > %d", r, len(k), limit)
		}
	}
}

// Lowering any keep value lets retention delete backups (so the Panel needs
// the owner's passkey for it), even if another value goes up; raising them
// doesn't.
func TestKeepsLess(t *testing.T) {
	cur := Retention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 4}
	for _, tc := range []struct {
		r    Retention
		want bool
	}{
		{cur, false},
		{Retention{KeepLast: 3, KeepDaily: 14, KeepWeekly: 4, KeepMonthly: 6}, false},
		{Retention{KeepLast: 2, KeepDaily: 7, KeepWeekly: 4}, true},
		{Retention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 3}, true},
		{Retention{KeepLast: 30, KeepDaily: 6, KeepWeekly: 4}, true},
	} {
		if got := tc.r.KeepsLess(cur); got != tc.want {
			t.Errorf("%+v.KeepsLess(%+v) = %v", tc.r, cur, got)
		}
	}
}
