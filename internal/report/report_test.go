package report

import (
	"testing"
	"time"

	"github.com/tanq16/expenseowl/internal/storage"
)

func TestBounds(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	date := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 12, 0, 0, 0, loc)
	}
	cases := []struct {
		name      string
		anchor    time.Time
		kind      Kind
		startDate int
		wantStart time.Time
		wantEnd   time.Time
	}{
		{"month before start", date(2026, 11, 4), Month, 5, time.Date(2026, 10, 5, 0, 0, 0, 0, loc), time.Date(2026, 11, 5, 0, 0, 0, 0, loc)},
		{"month on start", date(2026, 11, 5), Month, 5, time.Date(2026, 11, 5, 0, 0, 0, 0, loc), time.Date(2026, 12, 5, 0, 0, 0, 0, loc)},
		{"feb clamp", date(2026, 2, 15), Month, 31, time.Date(2026, 1, 31, 0, 0, 0, 0, loc), time.Date(2026, 2, 28, 0, 0, 0, 0, loc)},
		{"march clamp", date(2026, 3, 15), Month, 31, time.Date(2026, 2, 28, 0, 0, 0, 0, loc), time.Date(2026, 3, 31, 0, 0, 0, 0, loc)},
		{"leap feb", date(2024, 2, 15), Month, 31, time.Date(2024, 1, 31, 0, 0, 0, 0, loc), time.Date(2024, 2, 29, 0, 0, 0, 0, loc)},
		{"quarter before start", date(2026, 10, 4), Quarter, 5, time.Date(2026, 7, 5, 0, 0, 0, 0, loc), time.Date(2026, 10, 5, 0, 0, 0, 0, loc)},
		{"quarter on start", date(2026, 10, 5), Quarter, 5, time.Date(2026, 10, 5, 0, 0, 0, 0, loc), time.Date(2027, 1, 5, 0, 0, 0, 0, loc)},
		{"year before start", date(2026, 1, 4), Year, 5, time.Date(2025, 1, 5, 0, 0, 0, 0, loc), time.Date(2026, 1, 5, 0, 0, 0, 0, loc)},
		{"year on start", date(2026, 1, 5), Year, 5, time.Date(2026, 1, 5, 0, 0, 0, 0, loc), time.Date(2027, 1, 5, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Bounds(tc.anchor, tc.kind, tc.startDate, loc)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Start.Equal(tc.wantStart) || !p.End.Equal(tc.wantEnd) {
				t.Fatalf("got [%s, %s), want [%s, %s)", p.Start, p.End, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	period := Period{
		Kind:  Quarter,
		Start: time.Date(2026, 10, 5, 0, 0, 0, 0, loc),
		End:   time.Date(2027, 1, 5, 0, 0, 0, 0, loc),
		Label: "2026 Q4",
	}
	expenses := []storage.Expense{
		{Name: "午餐", Category: "Food", Amount: -100, Currency: "cny", Date: time.Date(2026, 10, 10, 12, 0, 0, 0, loc)},
		{Name: "工资", Category: "Income", Amount: 1000, Currency: "cny", Date: time.Date(2026, 11, 1, 12, 0, 0, 0, loc)},
		{Name: "房租", Category: "Rent", Amount: -300, Currency: "cny", Date: time.Date(2026, 12, 1, 12, 0, 0, 0, loc)},
		{Name: "范围外", Category: "Food", Amount: -999, Currency: "cny", Date: time.Date(2027, 1, 5, 0, 0, 0, 0, loc)},
	}
	r := Aggregate(expenses, period, Quarter, loc)
	if r.Totals.Income != 1000 || r.Totals.Expense != 400 || r.Totals.Balance != 600 {
		t.Fatalf("totals = %+v", r.Totals)
	}
	if len(r.Categories) != 3 {
		t.Fatalf("categories = %d", len(r.Categories))
	}
	var seriesExpense float64
	for _, b := range r.Series {
		seriesExpense += b.Expense
	}
	if seriesExpense != r.Totals.Expense {
		t.Fatalf("series expense = %v, total = %v", seriesExpense, r.Totals.Expense)
	}
	if len(r.Series) != 3 {
		t.Fatalf("series buckets = %d, want 3", len(r.Series))
	}
}

func TestBoundsRejectsInvalidStartDate(t *testing.T) {
	if _, err := Bounds(time.Now(), Month, 32, time.UTC); err == nil {
		t.Fatal("expected error")
	}
}
