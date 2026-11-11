// Command migrate-json-to-mysql copies the JSON ledger into an empty MySQL
// database. It is intentionally a one-way migration tool: the JSON files are
// read but never modified, so the old backup remains available.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tanq16/expenseowl/internal/storage"
)

type jsonLedger struct {
	Expenses []storage.Expense `json:"expenses"`
}

func main() {
	dataDir := flag.String("data", "data", "directory containing config.json and expenses.json")
	dryRun := flag.Bool("dry-run", false, "read and report counts without writing")
	skipConfig := flag.Bool("skip-config", false, "do not overwrite categories/currency/startDate")
	batchSize := flag.Int("batch", 500, "expenses per insert batch")
	flag.Parse()

	config, err := readConfig(filepath.Join(*dataDir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	ledger, err := readLedger(filepath.Join(*dataDir, "expenses.json"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("JSON source: %s\n", *dataDir)
	fmt.Printf("categories: %d, expenses: %d, recurring rules: %d\n",
		len(config.Categories), len(ledger.Expenses), len(config.RecurringExpenses))
	if *dryRun {
		fmt.Println("dry run: no database writes")
		return
	}
	if len(config.RecurringExpenses) > 0 {
		fmt.Println("warning: recurring rules are not migrated automatically; add them from the settings page")
	}

	systemConfig := storage.SystemConfig{}
	systemConfig.SetStorageConfig()
	if systemConfig.StorageType != storage.BackendTypeMySQL {
		log.Fatal("STORAGE_TYPE must be mysql; MYSQL_* variables must point to the target database")
	}
	store, err := storage.InitializeMySQLStore(systemConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	if !*skipConfig {
		if err := store.UpdateCategories(config.Categories); err != nil {
			log.Fatalf("update categories: %v", err)
		}
		if config.Currency != "" {
			if err := store.UpdateCurrency(config.Currency); err != nil {
				log.Fatalf("update currency: %v", err)
			}
		}
		if config.StartDate >= 1 && config.StartDate <= 31 {
			if err := store.UpdateStartDate(config.StartDate); err != nil {
				log.Fatalf("update start date: %v", err)
			}
		}
	}

	if *batchSize < 1 {
		*batchSize = 500
	}
	total := 0
	for start := 0; start < len(ledger.Expenses); start += *batchSize {
		end := start + *batchSize
		if end > len(ledger.Expenses) {
			end = len(ledger.Expenses)
		}
		if err := store.AddMultipleExpenses(ledger.Expenses[start:end]); err != nil {
			log.Fatalf("insert expenses %d-%d: %v", start+1, end, err)
		}
		total += end - start
		fmt.Printf("inserted %d/%d\n", total, len(ledger.Expenses))
	}
	fmt.Printf("migration complete: %d expenses\n", total)
}

func readConfig(path string) (*storage.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var config storage.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &config, nil
}

func readLedger(path string) (*jsonLedger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var ledger jsonLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return nil, fmt.Errorf("decode ledger: %w", err)
	}
	return &ledger, nil
}
