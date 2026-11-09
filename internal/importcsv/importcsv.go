// Package importcsv parses expense CSV files for the preview/commit flow.
// It keeps parsing and validation independent from HTTP and storage so the
// rules can be unit-tested without a running server.
package importcsv

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tanq16/expenseowl/internal/storage"
)

const maxFileBytes = 10 << 20

// Status describes one parsed row.
const (
	StatusValid     = "valid"
	StatusDuplicate = "duplicate"
	StatusInvalid   = "invalid"
)

// Profile contains the format choices exposed by the import UI.
type Profile struct {
	Delimiter       string `json:"delimiter"`
	DecimalComma    bool   `json:"decimalComma"`
	DefaultCategory string `json:"defaultCategory"`
	DefaultCurrency string `json:"defaultCurrency"`
	SkipDuplicates  bool   `json:"skipDuplicates"`
}

// Row is the preview view of one CSV record. Expense is kept out of JSON
// because it is only used by the commit path.
type Row struct {
	Line     int             `json:"line"`
	Status   string          `json:"status"`
	Message  string          `json:"message,omitempty"`
	Field    string          `json:"field,omitempty"`
	Name     string          `json:"name,omitempty"`
	Category string          `json:"category,omitempty"`
	Amount   float64         `json:"amount,omitempty"`
	Date     string          `json:"date,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
	Currency string          `json:"currency,omitempty"`
	Expense  storage.Expense `json:"-"`
}

// Preview is returned before anything is written.
type Preview struct {
	Total         int               `json:"total"`
	Valid         int               `json:"valid"`
	Duplicates    int               `json:"duplicates"`
	Invalid       int               `json:"invalid"`
	NewCategories []string          `json:"new_categories"`
	Rows          []Row             `json:"rows"`
	Expenses      []storage.Expense `json:"-"`
}

// Parse reads the CSV and classifies every data row. It does not write to
// storage. Existing expenses/categories are used for duplicate detection and
// for the "new categories" summary.
func Parse(r io.Reader, p Profile, existing []storage.Expense, existingCategories []string) (*Preview, error) {
	content, err := io.ReadAll(io.LimitReader(r, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(content) > maxFileBytes {
		return nil, fmt.Errorf("CSV is larger than 10MB")
	}
	content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})

	delim, err := chooseDelimiter(content, p.Delimiter)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(bytes.NewReader(content))
	reader.Comma = delim
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV must contain a header and at least one data row")
	}

	header := normalizeHeader(records[0])
	col := make(map[string]int, len(header))
	for i, name := range header {
		if name == "" {
			continue
		}
		if _, exists := col[name]; exists {
			return nil, fmt.Errorf("duplicate column: %s", name)
		}
		col[name] = i
	}
	for _, required := range []string{"name", "amount", "date"} {
		if _, ok := col[required]; !ok {
			return nil, fmt.Errorf("missing required column: %s", required)
		}
	}
	if _, ok := col["category"]; !ok && strings.TrimSpace(p.DefaultCategory) == "" {
		return nil, fmt.Errorf("missing required column: category")
	}

	existingIDs := make(map[string]struct{}, len(existing))
	fingerprintCount := make(map[string]int, len(existing))
	for _, e := range existing {
		if e.ID != "" {
			existingIDs[e.ID] = struct{}{}
		}
		fingerprintCount[fingerprint(e)]++
	}

	knownCategories := make(map[string]struct{}, len(existingCategories))
	for _, c := range existingCategories {
		knownCategories[strings.ToLower(c)] = struct{}{}
	}

	preview := &Preview{}
	for i, record := range records[1:] {
		line := i + 2
		if blankRecord(record) {
			continue
		}
		preview.Total++

		row := Row{Line: line, Status: StatusInvalid}
		if len(record) != len(header) {
			row.Message = fmt.Sprintf("expected %d columns, got %d", len(header), len(record))
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		field := func(name string) string {
			idx, ok := col[name]
			if !ok || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}

		category := field("category")
		if category == "" {
			category = strings.TrimSpace(p.DefaultCategory)
		}
		cleanCategory, err := storage.ValidateCategory(category)
		if err != nil {
			row.Field = "category"
			row.Message = err.Error()
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		amount, err := parseAmount(field("amount"), p.DecimalComma)
		if err != nil {
			row.Field = "amount"
			row.Message = err.Error()
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		date, err := parseDate(field("date"))
		if err != nil {
			row.Field = "date"
			row.Message = err.Error()
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		currency := lowerCurrency(field("currency"))
		if currency == "" {
			currency = lowerCurrency(p.DefaultCurrency)
		}
		if currency == "" {
			currency = "usd"
		}
		if !validCurrency(currency) {
			row.Field = "currency"
			row.Message = "unsupported currency: " + currency
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		direction := strings.ToLower(field("type"))
		if direction == "" {
			direction = strings.ToLower(field("direction"))
		}
		switch direction {
		case "debit", "expense", "支出":
			amount = -math.Abs(amount)
		case "credit", "income", "收入":
			amount = math.Abs(amount)
		case "":
			// Keep the sign from the CSV.
		default:
			row.Field = "type"
			row.Message = "unknown transaction type: " + direction
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		tags := splitTags(field("tags"))
		id := field("id")
		expense := storage.Expense{
			ID:       id,
			Name:     field("name"),
			Category: cleanCategory,
			Amount:   amount,
			Currency: currency,
			Date:     date,
			Tags:     tags,
		}
		if err := expense.Validate(); err != nil {
			row.Field = "row"
			row.Message = err.Error()
			preview.Invalid++
			preview.Rows = append(preview.Rows, row)
			continue
		}

		row.Name = expense.Name
		row.Category = expense.Category
		row.Amount = expense.Amount
		row.Date = expense.Date.In(time.Local).Format("2006-01-02")
		row.Tags = expense.Tags
		row.Currency = expense.Currency
		row.Expense = expense

		if id != "" {
			if _, exists := existingIDs[id]; exists {
				row.Status = StatusDuplicate
				row.Message = "expense ID already exists"
				preview.Duplicates++
				preview.Rows = append(preview.Rows, row)
				continue
			}
			existingIDs[id] = struct{}{}
		} else if p.SkipDuplicates {
			fp := fingerprint(expense)
			if fingerprintCount[fp] > 0 {
				row.Status = StatusDuplicate
				row.Message = "same content already exists"
				preview.Duplicates++
				fingerprintCount[fp]--
				preview.Rows = append(preview.Rows, row)
				continue
			}
			fingerprintCount[fp]++
		}

		row.Status = StatusValid
		preview.Valid++
		preview.Expenses = append(preview.Expenses, expense)
		preview.Rows = append(preview.Rows, row)
		if _, known := knownCategories[strings.ToLower(expense.Category)]; !known {
			knownCategories[strings.ToLower(expense.Category)] = struct{}{}
			preview.NewCategories = append(preview.NewCategories, expense.Category)
		}
	}

	return preview, nil
}

func chooseDelimiter(content []byte, requested string) (rune, error) {
	switch requested {
	case "", "auto":
		line := content
		if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
			line = content[:idx]
		}
		counts := map[rune]int{',': 0, ';': 0, '\t': 0, '|': 0}
		quoted := false
		for _, b := range line {
			switch b {
			case '"':
				quoted = !quoted
			default:
				if !quoted {
					if _, ok := counts[rune(b)]; ok {
						counts[rune(b)]++
					}
				}
			}
		}
		best := ','
		bestCount := -1
		for _, candidate := range []rune{',', ';', '\t', '|'} {
			if counts[candidate] > bestCount {
				best = candidate
				bestCount = counts[candidate]
			}
		}
		return best, nil
	case "comma", ",":
		return ',', nil
	case "semicolon", ";":
		return ';', nil
	case "tab", "\\t":
		return '\t', nil
	case "pipe", "|":
		return '|', nil
	default:
		runes := []rune(requested)
		if len(runes) != 1 {
			return 0, fmt.Errorf("delimiter must be one character")
		}
		return runes[0], nil
	}
}

func normalizeHeader(header []string) []string {
	out := make([]string, len(header))
	for i, col := range header {
		col = strings.TrimPrefix(col, "\ufeff")
		col = strings.ToLower(strings.TrimSpace(col))
		out[i] = col
	}
	return out
}

func blankRecord(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func parseAmount(raw string, decimalComma bool) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("amount is empty")
	}
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
	}
	s = strings.NewReplacer(" ", "", "\u00a0", "", "$", "", "€", "", "£", "", "¥", "", "￥", "").Replace(s)
	if strings.HasPrefix(s, "+") {
		s = strings.TrimPrefix(s, "+")
	}

	comma := strings.LastIndex(s, ",")
	dot := strings.LastIndex(s, ".")
	switch {
	case comma >= 0 && dot >= 0:
		if comma > dot {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case comma >= 0:
		if decimalComma {
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case dot >= 0 && decimalComma:
		// With comma-decimal input, a lone dot is treated as a thousands
		// separator. Users who really mean 12.34 should choose dot decimals.
		s = strings.ReplaceAll(s, ".", "")
	}

	if strings.Count(s, ".") > 1 {
		return 0, fmt.Errorf("invalid amount: %s", raw)
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid amount: %s", raw)
	}
	if negative {
		value = -value
	}
	if value == 0 {
		return 0, fmt.Errorf("amount cannot be 0")
	}
	return value, nil
}

func parseDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, fmt.Errorf("date is empty")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006/01/02", "2006-1-2", "2006/1/2"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	for _, layout := range []string{"02.01.2006", "2.1.2006"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date: %s", s)
}

func splitTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func lowerCurrency(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validCurrency(value string) bool {
	for _, supported := range storage.SupportedCurrencies {
		if supported == value {
			return true
		}
	}
	return false
}

func fingerprint(e storage.Expense) string {
	tags := append([]string(nil), e.Tags...)
	sort.Strings(tags)
	payload := strings.Join([]string{
		strings.ToLower(strings.Join(strings.Fields(e.Name), " ")),
		strings.ToLower(strings.Join(strings.Fields(e.Category), " ")),
		strconv.FormatInt(int64(math.Round(e.Amount*100)), 10),
		e.Date.UTC().Format("2006-01-02"),
		strings.ToLower(strings.Join(tags, ",")),
		strings.ToLower(e.Currency),
	}, "\x1f")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
