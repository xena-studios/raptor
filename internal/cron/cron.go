// Package cron parses standard five-field cron expressions and computes
// their run times in a time zone (docs/WINGS.md#scheduler). Wings uses it to
// fire schedules; the Panel uses it to validate them and show the next run.
//
// Daylight saving time, decided here rather than left to chance:
//   - A run whose time is skipped when the clocks go forward (02:30 on the
//     night 02:00 becomes 03:00) runs once, right when the clocks change.
//   - A time that happens twice when the clocks go back runs once, the first
//     time, unless the schedule runs every hour: then it keeps its rhythm
//     through the repeated hour too.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones don't depend on the host's tzdata
)

// Schedule is a parsed cron expression in a time zone.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	// domStar and dowStar: the field started with "*". When both day fields
	// are restricted, a day matches either one (as in every cron since Vixie).
	domStar, dowStar bool
	loc              *time.Location
}

// searchYears bounds the search for the next run. Nine years covers the
// rarest schedules that can run at all (29 February on a given weekday
// only via "or", or plain 29 February across 2100, which isn't a leap year).
const searchYears = 9

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var (
	months = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	days   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

type field struct {
	name     string
	min, max int
	names    []string // names[i] means min+i
}

var fields = [5]field{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of month", 1, 31, nil},
	{"month", 1, 12, months},
	{"day of week", 0, 7, days}, // 7 is Sunday too
}

// ErrNeverRuns is returned for expressions that match no real date, like
// "0 0 30 2 *".
var ErrNeverRuns = errors.New("this schedule never runs")

// Parse parses a five-field expression (minute, hour, day of month, month,
// day of week) or a macro (@hourly, @daily, @weekly, @monthly, @yearly) in
// the given IANA time zone ("" = UTC).
func Parse(expr, timezone string) (*Schedule, error) {
	loc, err := LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	expr = strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(expr)]; ok {
		expr = m
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron expression %q: want 5 fields (minute hour day-of-month month day-of-week), got %d", expr, len(parts))
	}
	s := &Schedule{loc: loc}
	sets := [5]*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, p := range parts {
		if *sets[i], err = parseField(p, fields[i]); err != nil {
			return nil, err
		}
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1
		s.dow &^= 1 << 7
	}
	s.domStar = strings.HasPrefix(parts[2], "*")
	s.dowStar = strings.HasPrefix(parts[4], "*")
	if s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		return nil, fmt.Errorf("cron expression %q: %w", expr, ErrNeverRuns)
	}
	return s, nil
}

// LoadLocation loads an IANA time zone. "" is UTC; "Local" isn't allowed,
// so a schedule means the same thing on every node.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	if name == "Local" {
		return nil, errors.New(`time zone "Local" isn't allowed; use an IANA name like "Europe/Berlin"`)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	return loc, nil
}

// parseField parses one comma-separated field: *, n, a-b, with an optional
// /step on * (every step), a-b, or n (from n to the end).
func parseField(s string, f field) (uint64, error) {
	var set uint64
	for part := range strings.SplitSeq(s, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 || n > f.max {
				return 0, fmt.Errorf("%s: bad step %q", f.name, part)
			}
			step = n
		}
		var lo, hi int
		switch {
		case rng == "*":
			lo, hi = f.min, f.max
			if f.max == 7 { // day of week: 0-6; 7 is only an alias
				hi = 6
			}
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = f.value(a); err != nil {
				return 0, err
			}
			if hi, err = f.value(b); err != nil {
				return 0, err
			}
			if hi < lo {
				return 0, fmt.Errorf("%s: range %q runs backwards", f.name, rng)
			}
		default:
			var err error
			if lo, err = f.value(rng); err != nil {
				return 0, err
			}
			hi = lo
			if hasStep {
				hi = f.max
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func (f field) value(s string) (int, error) {
	for i, n := range f.names {
		if strings.EqualFold(s, n) {
			return f.min + i, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("%s: %q isn't between %d and %d", f.name, s, f.min, f.max)
	}
	return v, nil
}

// Location is the schedule's time zone.
func (s *Schedule) Location() *time.Location { return s.loc }

// hourly reports whether the schedule runs in every hour of the day.
func (s *Schedule) hourly() bool { return s.hour == 1<<24-1 }

func has(set uint64, v int) bool { return set&(1<<v) != 0 }

func (s *Schedule) dayMatches(w time.Time) bool {
	dom, dow := has(s.dom, w.Day()), has(s.dow, int(w.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// matches reports whether a wall-clock time matches.
func (s *Schedule) matches(w time.Time) bool {
	return has(s.month, int(w.Month())) && s.dayMatches(w) && has(s.hour, w.Hour()) && has(s.minute, w.Minute())
}

// Next returns the first run strictly after t, or the zero time if there is
// none within searchYears.
func (s *Schedule) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(searchYears, 0, 0)
	for t.Before(limit) {
		w := t.In(s.loc)
		start, end := w.ZoneBounds()
		shift := zoneShift(start, w)

		// The clocks went forward at t: a run in the skipped span runs now.
		if shift > 0 && t.Equal(start) {
			wall := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, time.UTC)
			for m := time.Minute; m <= shift; m += time.Minute {
				if s.matches(wall.Add(-m)) {
					return t
				}
			}
		}
		// The clocks went back: the first shift after start repeats wall
		// times that already happened.
		repeat := shift < 0 && t.Before(start.Add(-shift))
		if s.matches(w) && (!repeat || s.hourly()) {
			return t
		}

		next := t.Add(s.skip(w))
		if repeat && !s.hourly() && next.Before(start.Add(-shift)) {
			next = start.Add(-shift)
		}
		if !end.IsZero() && next.After(end) {
			next = end
		}
		t = next
	}
	return time.Time{}
}

// skip is how far (in wall-clock terms) to move from a non-matching (or
// just matched) minute to the next one that could match.
func (s *Schedule) skip(w time.Time) time.Duration {
	wall := func(y int, mo time.Month, d, h, m int) time.Time { return time.Date(y, mo, d, h, m, 0, 0, time.UTC) }
	now := wall(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute())
	var to time.Time
	switch {
	case !has(s.month, int(w.Month())):
		to = wall(w.Year(), w.Month()+1, 1, 0, 0)
	case !s.dayMatches(w):
		to = wall(w.Year(), w.Month(), w.Day()+1, 0, 0)
	case !has(s.hour, w.Hour()):
		to = wall(w.Year(), w.Month(), w.Day(), w.Hour()+1, 0)
	default:
		to = now.Add(time.Minute)
	}
	return to.Sub(now)
}

// zoneShift is how much the clocks moved when the zone period containing w
// began (positive: forward). Zero for a zone without transitions.
func zoneShift(start, w time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	before := start.Add(-time.Nanosecond).In(w.Location())
	return offsetOf(w) - offsetOf(before)
}

func offsetOf(t time.Time) time.Duration {
	_, off := t.Zone()
	return time.Duration(off) * time.Second
}
