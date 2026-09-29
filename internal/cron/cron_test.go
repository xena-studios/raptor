package cron

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, expr, tz string) *Schedule {
	t.Helper()
	s, err := Parse(expr, tz)
	if err != nil {
		t.Fatalf("Parse(%q, %q): %v", expr, tz, err)
	}
	return s
}

func at(t *testing.T, tz, v string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04", v, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// runs returns the next n runs after from, formatted in the schedule's zone.
func runs(s *Schedule, from time.Time, n int) []string {
	var out []string
	for range n {
		from = s.Next(from)
		if from.IsZero() {
			break
		}
		out = append(out, from.In(s.loc).Format("2006-01-02 15:04 MST"))
	}
	return out
}

func TestParseErrors(t *testing.T) {
	for _, c := range []struct{ expr, tz, want string }{
		{"* * * *", "", "want 5 fields"},
		{"* * * * * *", "", "want 5 fields"},
		{"60 * * * *", "", "minute"},
		{"* 24 * * *", "", "hour"},
		{"* * 0 * *", "", "day of month"},
		{"* * * 13 *", "", "month"},
		{"* * * * 8", "", "day of week"},
		{"5-1 * * * *", "", "backwards"},
		{"*/0 * * * *", "", "bad step"},
		{"*/x * * * *", "", "bad step"},
		{"a * * * *", "", "minute"},
		{"1,,2 * * * *", "", "minute"},
		{"0 0 30 2 *", "", "never runs"},
		{"0 0 31 4,6,9,11 *", "", "never runs"},
		{"@reboot", "", "want 5 fields"},
		{"* * * * *", "Mars/Olympus", "unknown time zone"},
		{"* * * * *", "Local", "isn't allowed"},
	} {
		_, err := Parse(c.expr, c.tz)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q, %q) = %v, want an error containing %q", c.expr, c.tz, err, c.want)
		}
	}
	if _, err := Parse("0 0 30 2 *", ""); !errors.Is(err, ErrNeverRuns) {
		t.Errorf("want ErrNeverRuns, got %v", err)
	}
}

func TestNext(t *testing.T) {
	for _, c := range []struct {
		expr, tz, from string
		want           []string
	}{
		{"*/15 * * * *", "UTC", "2026-01-01 10:07", []string{"2026-01-01 10:15 UTC", "2026-01-01 10:30 UTC", "2026-01-01 10:45 UTC"}},
		{"0 4 * * *", "UTC", "2026-01-01 04:00", []string{"2026-01-02 04:00 UTC", "2026-01-03 04:00 UTC"}},
		{"@hourly", "", "2026-01-01 10:59", []string{"2026-01-01 11:00 UTC", "2026-01-01 12:00 UTC"}},
		{"@weekly", "", "2026-01-01 00:00", []string{"2026-01-04 00:00 UTC", "2026-01-11 00:00 UTC"}},
		{"@monthly", "", "2026-01-15 00:00", []string{"2026-02-01 00:00 UTC", "2026-03-01 00:00 UTC"}},
		{"@yearly", "", "2026-06-01 00:00", []string{"2027-01-01 00:00 UTC"}},
		{"0 12 * * mon-fri", "UTC", "2026-01-02 13:00", []string{"2026-01-05 12:00 UTC", "2026-01-06 12:00 UTC"}}, // Fri → Mon
		{"0 0 * * 7", "UTC", "2026-01-01 00:00", []string{"2026-01-04 00:00 UTC"}},                                // 7 = Sunday
		{"0 0 * JAN,Jul *", "UTC", "2026-01-31 00:00", []string{"2026-07-01 00:00 UTC", "2026-07-02 00:00 UTC"}},
		{"30 2 10-20/5 * *", "UTC", "2026-01-01 00:00", []string{"2026-01-10 02:30 UTC", "2026-01-15 02:30 UTC", "2026-01-20 02:30 UTC", "2026-02-10 02:30 UTC"}},
		{"50/5 * * * *", "UTC", "2026-01-01 00:00", []string{"2026-01-01 00:50 UTC", "2026-01-01 00:55 UTC", "2026-01-01 01:50 UTC"}},
		// Both day fields restricted: either matches (1st of the month or a Monday).
		{"0 0 1 * mon", "UTC", "2026-01-31 00:00", []string{"2026-02-01 00:00 UTC", "2026-02-02 00:00 UTC", "2026-02-09 00:00 UTC"}},
		// A starred day field means "and": */2 days that are also Mondays.
		{"0 0 */2 * mon", "UTC", "2026-01-01 00:00", []string{"2026-01-05 00:00 UTC", "2026-01-19 00:00 UTC"}},
		{"0 0 29 2 *", "UTC", "2026-01-01 00:00", []string{"2028-02-29 00:00 UTC", "2032-02-29 00:00 UTC"}},
		{"0 0 29 2 *", "UTC", "2097-01-01 00:00", []string{"2104-02-29 00:00 UTC"}}, // 2100 isn't a leap year

		// The time zone is the schedule's, not the host's.
		{"0 4 * * *", "Europe/Berlin", "2026-01-01 00:00", []string{"2026-01-01 04:00 CET"}},
		{"0 4 * * *", "Asia/Kathmandu", "2026-01-01 00:00", []string{"2026-01-01 04:00 +0545"}},

		// Clocks go forward in New York on 8 March 2026, 02:00 → 03:00.
		{"30 2 * * *", "America/New_York", "2026-03-07 03:00", []string{"2026-03-08 03:00 EDT", "2026-03-09 02:30 EDT"}},
		{"0 2 * * *", "America/New_York", "2026-03-07 03:00", []string{"2026-03-08 03:00 EDT", "2026-03-09 02:00 EDT"}},
		{"0 3 * * *", "America/New_York", "2026-03-07 04:00", []string{"2026-03-08 03:00 EDT", "2026-03-09 03:00 EDT"}}, // not twice
		{"*/30 * * * *", "America/New_York", "2026-03-08 01:00", []string{"2026-03-08 01:30 EST", "2026-03-08 03:00 EDT", "2026-03-08 03:30 EDT"}},
		{"0 1 * * *", "America/New_York", "2026-03-07 02:00", []string{"2026-03-08 01:00 EST", "2026-03-09 01:00 EDT"}},

		// Clocks go back in New York on 1 November 2026, 02:00 → 01:00.
		{"30 1 * * *", "America/New_York", "2026-10-31 02:00", []string{"2026-11-01 01:30 EDT", "2026-11-02 01:30 EST"}},
		{"*/30 * * * *", "America/New_York", "2026-11-01 00:45", []string{"2026-11-01 01:00 EDT", "2026-11-01 01:30 EDT", "2026-11-01 01:00 EST", "2026-11-01 01:30 EST", "2026-11-01 02:00 EST"}},
		{"0 2 * * *", "America/New_York", "2026-10-31 03:00", []string{"2026-11-01 02:00 EST", "2026-11-02 02:00 EST"}},
		{"30 1-2 * * *", "America/New_York", "2026-11-01 00:00", []string{"2026-11-01 01:30 EDT", "2026-11-01 02:30 EST"}},

		// Lord Howe Island moves its clocks by 30 minutes (5 April 2026, 02:00 → 01:30).
		{"45 1 * * *", "Australia/Lord_Howe", "2026-04-04 12:00", []string{"2026-04-05 01:45 +11", "2026-04-06 01:45 +1030"}},
		// …and forward on 4 October 2026, 02:00 → 02:30.
		{"15 2 * * *", "Australia/Lord_Howe", "2026-10-03 12:00", []string{"2026-10-04 02:30 +11", "2026-10-05 02:15 +11"}},

		// Chile moves its clocks at midnight: 6 September 2026, 00:00 → 01:00.
		{"30 0 * * *", "America/Santiago", "2026-09-05 12:00", []string{"2026-09-06 01:00 -03", "2026-09-07 00:30 -03"}},
		{"0 0 6 9 *", "America/Santiago", "2026-09-01 00:00", []string{"2026-09-06 01:00 -03"}},
	} {
		t.Run(c.expr+" "+c.tz+" "+c.from, func(t *testing.T) {
			s := mustParse(t, c.expr, c.tz)
			tz := c.tz
			if tz == "" {
				tz = "UTC"
			}
			got := runs(s, at(t, tz, c.from), len(c.want))
			if strings.Join(got, ", ") != strings.Join(c.want, ", ") {
				t.Errorf("runs:\n got %v\nwant %v", got, c.want)
			}
		})
	}
}

// reference computes runs the slow, obvious way: look at every minute.
// A minute fires if its wall clock matches (only the first time, unless the
// schedule is hourly), or if the clocks just jumped over a matching minute.
func reference(s *Schedule, after, until time.Time) []time.Time {
	wallOf := func(t time.Time) time.Time {
		w := t.In(s.loc)
		return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, time.UTC)
	}
	start := after.Truncate(time.Minute).Add(-6 * time.Hour)
	maxWall := wallOf(start)
	prevWall := maxWall
	var out []time.Time
	for t := start.Add(time.Minute); !t.After(until); t = t.Add(time.Minute) {
		wall := wallOf(t)
		fire := false
		for w := prevWall.Add(time.Minute); w.Before(wall); w = w.Add(time.Minute) {
			if s.matches(w) { // skipped by a jump forward
				fire = true
			}
		}
		repeat := !wall.After(maxWall)
		if s.matches(wall) && (!repeat || s.hourly()) {
			fire = true
		}
		if fire && t.After(after) {
			out = append(out, t)
		}
		if wall.After(maxWall) {
			maxWall = wall
		}
		prevWall = wall
	}
	return out
}

func randomExpr(r *rand.Rand) string {
	pick := func(lo, hi int) string {
		switch r.IntN(6) {
		case 0:
			return "*"
		case 1:
			return fmt.Sprintf("*/%d", 1+r.IntN(hi/2))
		case 2:
			a := lo + r.IntN(hi-lo+1)
			return fmt.Sprintf("%d-%d", a, a+r.IntN(hi-a+1))
		case 3:
			return fmt.Sprintf("%d,%d", lo+r.IntN(hi-lo+1), lo+r.IntN(hi-lo+1))
		default:
			return fmt.Sprint(lo + r.IntN(hi-lo+1))
		}
	}
	return strings.Join([]string{pick(0, 59), pick(0, 23), pick(1, 31), pick(1, 12), pick(0, 7)}, " ")
}

func TestNextMatchesReference(t *testing.T) {
	zones := []string{"UTC", "America/New_York", "Europe/London", "Australia/Lord_Howe", "America/Santiago", "Asia/Kathmandu", "Pacific/Chatham", "America/St_Johns"}
	// Around this year's clock changes, and some ordinary days.
	starts := []string{"2026-03-07 20:00", "2026-03-28 20:00", "2026-04-04 20:00", "2026-09-05 20:00", "2026-10-03 20:00", "2026-10-24 20:00", "2026-10-31 20:00", "2026-06-15 10:00"}
	r := rand.New(rand.NewPCG(1, 2))
	for range 400 {
		expr := randomExpr(r)
		// Half the cases keep days broad (many runs over two days); the rest
		// restrict days too, over five weeks, so whole days are skipped
		// across clock changes.
		f := strings.Fields(expr)
		window := 48 * time.Hour
		f[3] = "*"
		if r.IntN(2) == 0 {
			f[2], f[4] = "*", "*"
		} else {
			window = 35 * 24 * time.Hour
		}
		if r.IntN(3) == 0 {
			f[1] = "*"
		}
		expr = strings.Join(f, " ")
		tz := zones[r.IntN(len(zones))]
		s, err := Parse(expr, tz)
		if errors.Is(err, ErrNeverRuns) {
			continue
		} else if err != nil {
			t.Fatalf("Parse(%q): %v", expr, err)
		}
		from := at(t, tz, starts[r.IntN(len(starts))]).Add(time.Duration(r.IntN(600)) * time.Minute)
		until := from.Add(window)
		want := reference(s, from, until)
		var got []time.Time
		for n := s.Next(from); !n.IsZero() && !n.After(until); n = s.Next(n) {
			got = append(got, n)
		}
		for i := range max(len(got), len(want)) {
			var g, w time.Time
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if !g.Equal(w) {
				t.Fatalf("%q in %s from %v: run %d is %v, want %v", expr, tz, from, i, g.In(s.loc), w.In(s.loc))
			}
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"* * * * *", "*/5 1-3 1,15 jan-mar mon-fri", "@daily", "0 0 29 2 *", "5/10 * * * 7"} {
		f.Add(s, "America/New_York")
	}
	f.Fuzz(func(t *testing.T, expr, tz string) {
		s, err := Parse(expr, tz)
		if err != nil {
			return
		}
		from := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
		n := s.Next(from)
		if n.IsZero() {
			t.Fatalf("%q parsed but never runs", expr)
		}
		if !n.After(from) || n.Second() != 0 {
			t.Fatalf("Next(%v) = %v", from, n)
		}
	})
}
