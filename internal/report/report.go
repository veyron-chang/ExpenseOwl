// Package report turns a list of expenses into monthly, quarterly or yearly
// totals. Period boundaries are calculated once here instead of in the
// browser, which keeps the dashboard and the table consistent.
package report

import (
	"fmt"
	"sort"
	"time"
	_ "time/tzdata"

	"github.com/tanq16/expenseowl/internal/storage"
)

// Kind is the report granularity.
type Kind string

const (
	Month   Kind = "month"
	Quarter Kind = "quarter"
	Year    Kind = "year"
)

// Period is a half-open interval [Start, End).
type Period struct {
	Kind  Kind      `json:"kind"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Label string    `json:"label"`
}

// Totals is the income/expense summary for one period.
type Totals struct {
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
	Balance float64 `json:"balance"`
}

// Category is one row of the category breakdown.
type Category struct {
	Name    string  `json:"name"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
	Share   float64 `json:"share"`
}

// Bucket is one point in the report series.
type Bucket struct {
	Label   string  `json:"label"`
	Start   string  `json:"start"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
	Balance float64 `json:"balance"`
}

// Report is the JSON payload returned by GET /report.
type Report struct {
	Period     Period     `json:"period"`
	Totals     Totals     `json:"totals"`
	Categories []Category `json:"categories"`
	Series     []Bucket   `json:"series"`
}

// Bounds calculates the reporting period containing anchor. startDate is the
// configured day of the month (1..31); shorter months are clamped to their last
// day. The end is exclusive.
func Bounds(anchor time.Time, kind Kind, startDate int, loc *time.Location) (Period, error) {
	if startDate < 1 || startDate > 31 {
		return Period{}, fmt.Errorf("startDate must be between 1 and 31")
	}
	if loc == nil {
		loc = time.Local
	}
	a := anchor.In(loc)
	y, m, _ := a.Date()

	switch kind {
	case Month:
		start := monthStart(y, m, startDate, loc)
		if a.Before(start) {
			y, m = addMonths(y, m, -1)
			start = monthStart(y, m, startDate, loc)
		}
		ny, nm := addMonths(y, m, 1)
		return Period{Kind: kind, Start: start, End: monthStart(ny, nm, startDate, loc), Label: start.Format("2006-01")}, nil

	case Quarter:
		qm := time.Month(((int(m)-1)/3)*3 + 1)
		start := monthStart(y, qm, startDate, loc)
		if a.Before(start) {
			y, qm = addMonths(y, qm, -3)
			start = monthStart(y, qm, startDate, loc)
		}
		ny, nm := addMonths(y, qm, 3)
		label := fmt.Sprintf("%d Q%d", start.Year(), (int(start.Month())-1)/3+1)
		return Period{Kind: kind, Start: start, End: monthStart(ny, nm, startDate, loc), Label: label}, nil

	case Year:
		start := monthStart(y, time.January, startDate, loc)
		if a.Before(start) {
			start = monthStart(y-1, time.January, startDate, loc)
		}
		end := monthStart(start.Year()+1, time.January, startDate, loc)
		return Period{Kind: kind, Start: start, End: end, Label: fmt.Sprintf("%d", start.Year())}, nil

	default:
		return Period{}, fmt.Errorf("unknown period kind: %s", kind)
	}
}

// Aggregate computes totals, category shares and a complete series. Missing
// buckets are filled with zero so the chart has a continuous x-axis.
func Aggregate(expenses []storage.Expense, period Period, kind Kind, loc *time.Location) Report {
	if loc == nil {
		loc = time.Local
	}
	totals := Totals{}
	categoryMap := make(map[string]*Category)
	bucketMap := make(map[string]*Bucket)

	for _, e := range expenses {
		if e.Date.Before(period.Start) || !e.Date.Before(period.End) {
			continue
		}
		localDate := e.Date.In(loc)
		cat := categoryMap[e.Category]
		if cat == nil {
			cat = &Category{Name: e.Category}
			categoryMap[e.Category] = cat
		}
		label := bucketLabel(localDate, kind)
		bucket := bucketMap[label]
		if bucket == nil {
			bucket = &Bucket{Label: label, Start: bucketStart(localDate, kind)}
			bucketMap[label] = bucket
		}

		if e.Amount >= 0 {
			totals.Income += e.Amount
			cat.Income += e.Amount
			bucket.Income += e.Amount
		} else {
			value := -e.Amount
			totals.Expense += value
			cat.Expense += value
			bucket.Expense += value
		}
	}
	totals.Balance = totals.Income - totals.Expense

	categories := make([]Category, 0, len(categoryMap))
	for _, cat := range categoryMap {
		if totals.Expense > 0 {
			cat.Share = cat.Expense / totals.Expense
		}
		categories = append(categories, *cat)
	}
	sort.Slice(categories, func(i, j int) bool {
		if categories[i].Expense == categories[j].Expense {
			return categories[i].Name < categories[j].Name
		}
		return categories[i].Expense > categories[j].Expense
	})

	series := make([]Bucket, 0)
	for cursor := period.Start; cursor.Before(period.End); {
		label := bucketLabel(cursor, kind)
		if bucket, ok := bucketMap[label]; ok {
			bucket.Balance = bucket.Income - bucket.Expense
			series = append(series, *bucket)
		} else {
			series = append(series, Bucket{Label: label, Start: bucketStart(cursor, kind)})
		}
		if kind == Month {
			cursor = cursor.AddDate(0, 0, 1)
		} else {
			cursor = cursor.AddDate(0, 1, 0)
		}
	}

	return Report{Period: period, Totals: totals, Categories: categories, Series: series}
}

func monthStart(year int, month time.Month, startDate int, loc *time.Location) time.Time {
	day := startDate
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

func addMonths(year int, month time.Month, delta int) (int, time.Month) {
	idx := year*12 + int(month) - 1 + delta
	return idx / 12, time.Month(idx%12 + 1)
}

func bucketLabel(t time.Time, kind Kind) string {
	if kind == Month {
		return t.Format("01-02")
	}
	return t.Format("2006-01")
}

func bucketStart(t time.Time, kind Kind) string {
	if kind == Month {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01") + "-01"
}
