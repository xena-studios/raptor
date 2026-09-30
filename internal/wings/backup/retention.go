package backup

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Retention says which backups a server keeps. Each rule keeps the newest
// backup of each of its last N periods that have a backup, so a server that
// was offline for a month doesn't lose its dailies to the calendar. A
// backup kept by any rule is kept. At least one rule must keep something:
// backups are never kept forever by accident.
type Retention struct {
	KeepLast    int `json:"keep_last"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// DefaultRetention: backups are deduplicated, so the older ones cost little
// and catch problems noticed late (griefing found a week later).
var DefaultRetention = Retention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 4}

const maxKeep = 1000

func (r Retention) validate() error {
	for _, n := range []int{r.KeepLast, r.KeepDaily, r.KeepWeekly, r.KeepMonthly} {
		if n < 0 || n > maxKeep {
			return fmt.Errorf("keep values must be between 0 and %d", maxKeep)
		}
	}
	if r == (Retention{}) {
		return errors.New("at least one keep value must be above zero")
	}
	return nil
}

// candidate is a finished backup retention may delete.
type candidate struct {
	ID string
	At time.Time
}

// expired returns the IDs of the backups no rule keeps. Days, weeks (ISO),
// and months are counted in loc.
func (r Retention) expired(backups []candidate, loc *time.Location) []string {
	if r == (Retention{}) {
		return nil // invalid; never read as "delete everything"
	}
	sorted := slices.Clone(backups)
	slices.SortStableFunc(sorted, func(a, b candidate) int { return b.At.Compare(a.At) }) // newest first
	keep := make([]bool, len(sorted))
	for i := range min(r.KeepLast, len(sorted)) {
		keep[i] = true
	}
	period := func(n int, key func(time.Time) string) {
		seen := map[string]bool{}
		for i, b := range sorted {
			if len(seen) == n {
				return
			}
			k := key(b.At.In(loc))
			if !seen[k] {
				seen[k] = true
				keep[i] = true
			}
		}
	}
	period(r.KeepDaily, func(t time.Time) string { return t.Format(time.DateOnly) })
	period(r.KeepWeekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-%d", y, w)
	})
	period(r.KeepMonthly, func(t time.Time) string { return t.Format("2006-01") })
	var out []string
	for i, b := range sorted {
		if !keep[i] {
			out = append(out, b.ID)
		}
	}
	return out
}

// KeepsLess reports whether r would let retention delete backups old keeps:
// some keep value is lower.
func (r Retention) KeepsLess(old Retention) bool {
	return r.KeepLast < old.KeepLast || r.KeepDaily < old.KeepDaily ||
		r.KeepWeekly < old.KeepWeekly || r.KeepMonthly < old.KeepMonthly
}
