package billing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/shared/hosted"
)

func TestStorageOver(t *testing.T) {
	gib := int64(1) << 30
	for _, c := range []struct {
		used  int64
		nodes int
		want  int64
	}{
		{0, 1, 0},
		{9 * gib, 1, 0},
		{10 * gib, 1, 0},
		{25 * gib, 1, 15 * gib},
		{25 * gib, 3, 0},
		{25 * gib, 0, 25 * gib},
	} {
		if got := StorageOver(c.used, c.nodes); got != c.want {
			t.Errorf("%d bytes on %d nodes: %d over, want %d", c.used, c.nodes, got, c.want)
		}
	}
}

// A month of days adds up to the month's GB-months, whatever its length,
// so a TB stored all month is $12 at 1.2 cents a GB-month.
func TestGBMonths(t *testing.T) {
	for _, month := range []time.Month{time.February, time.April, time.October} {
		var sum float64
		start := time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC)
		for d := start; d.Month() == month; d = d.AddDate(0, 0, 1) {
			sum += GBMonths(1e12, d)
		}
		if sum < 999.999 || sum > 1000.001 {
			t.Errorf("%s: %f GB-months for a TB all month", month, sum)
		}
		if cents := sum * 1.2; cents < hosted.CentsPerTB-0.01 || cents > hosted.CentsPerTB+0.01 {
			t.Errorf("%s: %f cents", month, cents)
		}
	}
	if got := GBMonths(31e9, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)); got != 1 {
		t.Errorf("31 GB for a day of October: %v GB-months", got)
	}
}

func TestMetered(t *testing.T) {
	var s Subscription
	if err := json.Unmarshal([]byte(`{
		"prices": [{"amount_type": "seat_based"}, {"amount_type": "metered_unit", "meter_id": "m1"}],
		"meters": [{"meter_id": "m1", "amount": 42, "consumed_units": 35}, {"meter_id": "m2", "amount": 7}]
	}`), &s); err != nil {
		t.Fatal(err)
	}
	if priced, cents := s.Metered("m1"); !priced || cents != 42 {
		t.Errorf("m1: %v, %d", priced, cents)
	}
	if priced, cents := s.Metered("m2"); priced || cents != 7 {
		t.Errorf("a meter without a price: %v, %d", priced, cents)
	}
	if priced, cents := s.Metered(""); priced || cents != 0 {
		t.Errorf("no meter set: %v, %d", priced, cents)
	}
}
