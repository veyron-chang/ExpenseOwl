package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tanq16/expenseowl/internal/report"
)

// GetReport returns server-side totals for a month, quarter or year.
func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "Method not allowed"})
		return
	}

	kind := report.Kind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))))
	if kind == "" {
		kind = report.Month
	}
	if kind != report.Month && kind != report.Quarter && kind != report.Year {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "period must be month, quarter or year"})
		return
	}

	loc, err := loadLocation(r.URL.Query().Get("tz"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	anchor, err := parseAnchor(r.URL.Query().Get("anchor"), loc)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	startDate, err := h.storage.GetStartDate()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to read start date"})
		return
	}
	period, err := report.Bounds(anchor, kind, startDate, loc)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	expenses, err := h.storage.GetExpensesByDateRange(period.Start, period.End)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to read expenses"})
		log.Printf("API ERROR: report query: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, report.Aggregate(expenses, period, kind, loc))
}

func loadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone: %s", name)
	}
	return loc, nil
}

func parseAnchor(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().In(loc), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.In(loc), nil
	}
	return time.Time{}, fmt.Errorf("anchor must be YYYY-MM-DD or RFC3339")
}

// GetExpenses supports the existing no-parameter call plus the optional
// from/to date-range query used by the table page.
func (h *Handler) GetExpenses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "Method not allowed"})
		return
	}
	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	if fromRaw == "" && toRaw == "" {
		h.getAllExpenses(w, r)
		return
	}
	if fromRaw == "" || toRaw == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "from and to must be provided together"})
		return
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "from must be RFC3339"})
		return
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "to must be RFC3339"})
		return
	}
	if !from.Before(to) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "from must be before to"})
		return
	}
	expenses, err := h.storage.GetExpensesByDateRange(from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to retrieve expenses"})
		log.Printf("API ERROR: date-range expenses: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, expenses)
}

func (h *Handler) getAllExpenses(w http.ResponseWriter, _ *http.Request) {
	expenses, err := h.storage.GetAllExpenses()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to retrieve expenses"})
		log.Printf("API ERROR: Failed to retrieve expenses: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, expenses)
}
