package importcsv

import (
	"strings"
	"testing"
	"time"

	"github.com/tanq16/expenseowl/internal/storage"
)

func TestParseSemicolonDecimalAndDefaultCategory(t *testing.T) {
	input := "\ufeffname;amount;date;id\n午餐;1.234,56;2026-11-08;id-1\n"
	p := Profile{
		Delimiter:       ";",
		DecimalComma:    true,
		DefaultCategory: "Food",
		DefaultCurrency: "cny",
		SkipDuplicates:  true,
	}
	preview, err := Parse(strings.NewReader(input), p, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Valid != 1 || preview.Invalid != 0 {
		t.Fatalf("counts = valid:%d invalid:%d", preview.Valid, preview.Invalid)
	}
	got := preview.Expenses[0]
	if got.Amount != 1234.56 {
		t.Errorf("amount = %v", got.Amount)
	}
	if got.Category != "Food" {
		t.Errorf("category = %q", got.Category)
	}
	if got.ID != "id-1" {
		t.Errorf("id = %q", got.ID)
	}
	if got.Currency != "cny" {
		t.Errorf("currency = %q", got.Currency)
	}
}

func TestParseDuplicateIdIsSkipped(t *testing.T) {
	existing := []storage.Expense{{
		ID:       "same-id",
		Name:     "午餐",
		Category: "Food",
		Amount:   -20,
		Currency: "usd",
		Date:     time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
	}}
	input := "id,name,category,amount,date\nsame-id,午餐,Food,-20,2026-11-08\n"
	preview, err := Parse(strings.NewReader(input), Profile{Delimiter: ",", DefaultCurrency: "usd"}, existing, []string{"Food"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Duplicates != 1 || preview.Valid != 0 {
		t.Fatalf("counts = duplicates:%d valid:%d", preview.Duplicates, preview.Valid)
	}
}

func TestParseContentFingerprintMultiset(t *testing.T) {
	date := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	existing := []storage.Expense{{
		ID:       "old",
		Name:     "咖啡",
		Category: "Food",
		Amount:   -20,
		Currency: "usd",
		Date:     date,
	}}
	input := "name,category,amount,date\n咖啡,Food,-20,2026-11-08\n咖啡,Food,-20,2026-11-08\n"
	preview, err := Parse(strings.NewReader(input), Profile{Delimiter: ",", DefaultCurrency: "usd", SkipDuplicates: true}, existing, []string{"Food"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Duplicates != 1 || preview.Valid != 1 {
		t.Fatalf("counts = duplicates:%d valid:%d", preview.Duplicates, preview.Valid)
	}
}

func TestParseRejectsBadAmountAndDate(t *testing.T) {
	input := "name,category,amount,date\n午餐,Food,abc,2026-11-08\n晚餐,Food,-20,not-a-date\n"
	preview, err := Parse(strings.NewReader(input), Profile{Delimiter: ",", DefaultCurrency: "usd"}, nil, []string{"Food"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Invalid != 2 {
		t.Fatalf("invalid = %d", preview.Invalid)
	}
}

func TestParseDirectionColumn(t *testing.T) {
	input := "name,category,amount,date,type\n工资,Income,8000,2026-11-01,credit\n房租,Rent,3000,2026-11-02,debit\n"
	preview, err := Parse(strings.NewReader(input), Profile{Delimiter: ",", DefaultCurrency: "usd"}, nil, []string{"Income", "Rent"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Valid != 2 {
		t.Fatalf("valid = %d", preview.Valid)
	}
	if preview.Expenses[0].Amount != 8000 || preview.Expenses[1].Amount != -3000 {
		t.Errorf("amounts = %v, %v", preview.Expenses[0].Amount, preview.Expenses[1].Amount)
	}
}
