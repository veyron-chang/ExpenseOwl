package api

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/tanq16/expenseowl/internal/importcsv"
)

// PreviewCSV parses an upload and returns row-level results without writing.
func (h *Handler) PreviewCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "Method not allowed"})
		return
	}
	profile, data, _, err := readImportRequest(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	preview, err := h.parseCSV(data, profile)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// CommitCSV parses the same upload again and writes only the valid rows.
// Re-parsing avoids keeping a server-side temporary file between preview and
// confirmation.
func (h *Handler) CommitCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "Method not allowed"})
		return
	}
	profile, data, _, err := readImportRequest(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	preview, err := h.parseCSV(data, profile)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	if len(preview.Expenses) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"total":          preview.Total,
			"imported":       0,
			"duplicates":     preview.Duplicates,
			"invalid":        preview.Invalid,
			"new_categories": preview.NewCategories,
		})
		return
	}

	categories, err := h.storage.GetCategories()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not read categories"})
		return
	}
	merged := mergeCategories(categories, preview.NewCategories)
	if len(merged) != len(categories) {
		if err := h.storage.UpdateCategories(merged); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not update categories"})
			log.Printf("API ERROR: update categories after import: %v", err)
			return
		}
	}

	if err := h.storage.AddMultipleExpenses(preview.Expenses); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to import expenses"})
		log.Printf("API ERROR: commit CSV: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":          preview.Total,
		"imported":       len(preview.Expenses),
		"duplicates":     preview.Duplicates,
		"invalid":        preview.Invalid,
		"new_categories": preview.NewCategories,
	})
}

func (h *Handler) parseCSV(data []byte, profile importcsv.Profile) (*importcsv.Preview, error) {
	expenses, err := h.storage.GetAllExpenses()
	if err != nil {
		return nil, fmt.Errorf("could not read existing expenses: %w", err)
	}
	categories, err := h.storage.GetCategories()
	if err != nil {
		return nil, fmt.Errorf("could not read existing categories: %w", err)
	}
	currency, err := h.storage.GetCurrency()
	if err != nil {
		return nil, fmt.Errorf("could not read currency: %w", err)
	}
	if profile.DefaultCurrency == "" {
		profile.DefaultCurrency = currency
	}
	return importcsv.Parse(bytes.NewReader(data), profile, expenses, categories)
}

func readImportRequest(w http.ResponseWriter, r *http.Request) (importcsv.Profile, []byte, string, error) {
	var profile importcsv.Profile
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		return profile, nil, "", fmt.Errorf("could not parse multipart form: %w", err)
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return profile, nil, "", fmt.Errorf("could not read uploaded file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil {
		return profile, nil, "", fmt.Errorf("could not read file: %w", err)
	}
	if len(data) > 10<<20 {
		return profile, nil, "", fmt.Errorf("file is larger than 10MB")
	}

	profile.Delimiter = strings.TrimSpace(r.FormValue("delimiter"))
	profile.DecimalComma = r.FormValue("decimal") == "comma"
	profile.DefaultCategory = strings.TrimSpace(r.FormValue("default_category"))
	profile.SkipDuplicates = r.FormValue("skip_duplicates") != "false"

	name := ""
	if header != nil {
		name = header.Filename
	}
	return profile, data, name, nil
}

func mergeCategories(current, added []string) []string {
	seen := make(map[string]struct{}, len(current))
	out := append([]string(nil), current...)
	for _, item := range current {
		seen[strings.ToLower(item)] = struct{}{}
	}
	for _, item := range added {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
