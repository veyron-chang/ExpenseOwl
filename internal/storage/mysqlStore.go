package storage

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// mysqlStore implements Storage on MySQL 8. The schema separates categories
// and tags into their own tables so the database has real foreign keys instead
// of JSON blobs used by the PostgreSQL backend.
type mysqlStore struct {
	db       *sql.DB
	defaults map[string]string
}

// InitializeMySQLStore opens the connection, creates missing tables and seeds
// the default configuration/categories.
func InitializeMySQLStore(baseConfig SystemConfig) (Storage, error) {
	dsn, err := makeMySQLDSN(baseConfig)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL database: %v", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping MySQL database: %v", err)
	}
	if err := createMySQLSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	store := &mysqlStore{db: db, defaults: map[string]string{}}
	config, err := store.GetConfig()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load MySQL config: %v", err)
	}
	store.defaults["currency"] = config.Currency
	store.defaults["start_date"] = fmt.Sprintf("%d", config.StartDate)
	log.Println("Connected to MySQL database")
	return store, nil
}

func makeMySQLDSN(c SystemConfig) (string, error) {
	raw := strings.TrimSpace(c.StorageURL)
	if raw == "" {
		return "", fmt.Errorf("STORAGE_URL is empty")
	}
	addr := raw
	dbName := "expenseowl"
	if idx := strings.Index(raw, "/"); idx >= 0 {
		addr = raw[:idx]
		dbName = raw[idx+1:]
	}
	if !strings.Contains(addr, ":") {
		addr += ":3306"
	}
	cfg := mysql.NewConfig()
	cfg.User = c.StorageUser
	cfg.Passwd = c.StoragePass
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.DBName = dbName
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = "utf8mb4_0900_ai_ci"
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	return cfg.FormatDSN(), nil
}

var mysqlSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS app_config (
		id TINYINT NOT NULL DEFAULT 1,
		currency CHAR(3) NOT NULL,
		start_date TINYINT UNSIGNED NOT NULL,
		locale VARCHAR(10) NOT NULL DEFAULT 'zh-CN',
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
		PRIMARY KEY (id),
		CONSTRAINT chk_config_single CHECK (id = 1),
		CONSTRAINT chk_config_start_date CHECK (start_date BETWEEN 1 AND 31)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS categories (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
		name VARCHAR(64) NOT NULL,
		sort_order INT NOT NULL DEFAULT 0,
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		PRIMARY KEY (id),
		UNIQUE KEY uq_categories_name (name)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS recurring_expenses (
		id CHAR(36) NOT NULL,
		name VARCHAR(255) NOT NULL,
		category_id BIGINT UNSIGNED NOT NULL,
		amount DECIMAL(12,2) NOT NULL,
		currency CHAR(3) NOT NULL,
		repeat_interval ENUM('daily','weekly','monthly','yearly') NOT NULL,
		occurrences INT NOT NULL,
		start_date DATETIME(6) NOT NULL,
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
		PRIMARY KEY (id),
		KEY idx_recurring_start (start_date),
		CONSTRAINT fk_recurring_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS expenses (
		id CHAR(36) NOT NULL,
		recurring_id CHAR(36) NULL,
		name VARCHAR(255) NOT NULL,
		category_id BIGINT UNSIGNED NOT NULL,
		amount DECIMAL(12,2) NOT NULL,
		currency CHAR(3) NOT NULL,
		occurred_at DATETIME(6) NOT NULL,
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
		PRIMARY KEY (id),
		KEY idx_expenses_date (occurred_at),
		KEY idx_expenses_category (category_id),
		KEY idx_expenses_recurring (recurring_id),
		CONSTRAINT fk_expenses_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT,
		CONSTRAINT fk_expenses_recurring FOREIGN KEY (recurring_id) REFERENCES recurring_expenses(id) ON DELETE SET NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS tags (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
		name VARCHAR(64) NOT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uq_tags_name (name)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS expense_tags (
		expense_id CHAR(36) NOT NULL,
		tag_id BIGINT UNSIGNED NOT NULL,
		PRIMARY KEY (expense_id, tag_id),
		KEY idx_expense_tags_tag (tag_id),
		CONSTRAINT fk_expense_tags_expense FOREIGN KEY (expense_id) REFERENCES expenses(id) ON DELETE CASCADE,
		CONSTRAINT fk_expense_tags_tag FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS import_batches (
		id CHAR(36) NOT NULL,
		file_name VARCHAR(255) NOT NULL,
		file_hash CHAR(64) NOT NULL,
		profile JSON NOT NULL,
		status ENUM('previewed','committed','failed','expired') NOT NULL,
		total_rows INT NOT NULL DEFAULT 0,
		valid_rows INT NOT NULL DEFAULT 0,
		duplicate_rows INT NOT NULL DEFAULT 0,
		invalid_rows INT NOT NULL DEFAULT 0,
		created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		committed_at DATETIME(6) NULL,
		PRIMARY KEY (id),
		KEY idx_import_batches_status (status)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS import_rows (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
		batch_id CHAR(36) NOT NULL,
		line_no INT NOT NULL,
		raw_data JSON NOT NULL,
		fingerprint CHAR(64) NULL,
		status ENUM('valid','duplicate','invalid','imported') NOT NULL,
		error_code VARCHAR(64) NULL,
		message VARCHAR(255) NULL,
		expense_id CHAR(36) NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uq_import_rows_line (batch_id, line_no),
		KEY idx_import_rows_status (batch_id, status),
		CONSTRAINT fk_import_rows_batch FOREIGN KEY (batch_id) REFERENCES import_batches(id) ON DELETE CASCADE,
		CONSTRAINT fk_import_rows_expense FOREIGN KEY (expense_id) REFERENCES expenses(id) ON DELETE SET NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,

	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(32) NOT NULL,
		applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
}

func createMySQLSchema(db *sql.DB) error {
	for _, statement := range mysqlSchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("failed to create MySQL schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO app_config (id, currency, start_date) VALUES (1, 'usd', 1)
		ON DUPLICATE KEY UPDATE id = id`); err != nil {
		return fmt.Errorf("failed to seed app_config: %v", err)
	}
	for i, category := range defaultCategories {
		if _, err := db.Exec(`INSERT IGNORE INTO categories (name, sort_order) VALUES (?, ?)`, category, i); err != nil {
			return fmt.Errorf("failed to seed category %s: %v", category, err)
		}
	}
	_, _ = db.Exec(`INSERT IGNORE INTO schema_migrations (version) VALUES ('001_init')`)
	return nil
}

func (s *mysqlStore) Close() error { return s.db.Close() }

func (s *mysqlStore) GetConfig() (*Config, error) {
	config := &Config{}
	err := s.db.QueryRow(`SELECT currency, start_date FROM app_config WHERE id = 1`).Scan(&config.Currency, &config.StartDate)
	if err != nil {
		return nil, fmt.Errorf("failed to read MySQL config: %v", err)
	}
	categories, err := s.GetCategories()
	if err != nil {
		return nil, err
	}
	recurring, err := s.GetRecurringExpenses()
	if err != nil {
		return nil, err
	}
	config.Categories = categories
	config.RecurringExpenses = recurring
	return config, nil
}

func (s *mysqlStore) GetCategories() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM categories ORDER BY sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query categories: %v", err)
	}
	defer rows.Close()
	var categories []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		categories = append(categories, name)
	}
	return categories, nil
}

func (s *mysqlStore) UpdateCategories(categories []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	keep := make(map[string]struct{}, len(categories))
	for i, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}
		keep[strings.ToLower(category)] = struct{}{}
		if _, err := tx.Exec(`INSERT INTO categories (name, sort_order) VALUES (?, ?)
			ON DUPLICATE KEY UPDATE sort_order = VALUES(sort_order)`, category, i); err != nil {
			return fmt.Errorf("failed to upsert category %s: %v", category, err)
		}
	}

	rows, err := tx.Query(`SELECT id, name FROM categories`)
	if err != nil {
		return err
	}
	type categoryRow struct {
		id   int64
		name string
	}
	var old []categoryRow
	for rows.Next() {
		var c categoryRow
		if err := rows.Scan(&c.id, &c.name); err != nil {
			rows.Close()
			return err
		}
		old = append(old, c)
	}
	rows.Close()
	for _, c := range old {
		if _, ok := keep[strings.ToLower(c.name)]; ok {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM categories WHERE id = ?`, c.id); err != nil {
			return fmt.Errorf("category %s is still in use: %v", c.name, err)
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) GetCurrency() (string, error) {
	var currency string
	err := s.db.QueryRow(`SELECT currency FROM app_config WHERE id = 1`).Scan(&currency)
	return currency, err
}

func (s *mysqlStore) UpdateCurrency(currency string) error {
	for _, supported := range SupportedCurrencies {
		if supported == currency {
			_, err := s.db.Exec(`UPDATE app_config SET currency = ? WHERE id = 1`, currency)
			s.defaults["currency"] = currency
			return err
		}
	}
	return fmt.Errorf("invalid currency: %s", currency)
}

func (s *mysqlStore) GetStartDate() (int, error) {
	var startDate int
	err := s.db.QueryRow(`SELECT start_date FROM app_config WHERE id = 1`).Scan(&startDate)
	return startDate, err
}

func (s *mysqlStore) UpdateStartDate(startDate int) error {
	if startDate < 1 || startDate > 31 {
		return fmt.Errorf("invalid start date: %d", startDate)
	}
	_, err := s.db.Exec(`UPDATE app_config SET start_date = ? WHERE id = 1`, startDate)
	s.defaults["start_date"] = fmt.Sprintf("%d", startDate)
	return err
}

func (s *mysqlStore) queryExpenses(query string, args ...any) ([]Expense, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var expenses []Expense
	for rows.Next() {
		expense, err := scanMySQLExpense(rows)
		if err != nil {
			return nil, err
		}
		expenses = append(expenses, expense)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachTags(expenses)
}

const expenseSelect = `SELECT e.id, e.recurring_id, e.name, c.name, e.amount, e.currency, e.occurred_at
	FROM expenses e JOIN categories c ON c.id = e.category_id`

func (s *mysqlStore) GetAllExpenses() ([]Expense, error) {
	return s.queryExpenses(expenseSelect + ` ORDER BY e.occurred_at DESC`)
}

func (s *mysqlStore) GetExpensesByDateRange(from, to time.Time) ([]Expense, error) {
	return s.queryExpenses(expenseSelect+` WHERE e.occurred_at >= ? AND e.occurred_at < ? ORDER BY e.occurred_at DESC`, from, to)
}

func (s *mysqlStore) GetExpense(id string) (Expense, error) {
	rows, err := s.db.Query(expenseSelect+` WHERE e.id = ?`, id)
	if err != nil {
		return Expense{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Expense{}, fmt.Errorf("expense with ID %s not found", id)
	}
	expense, err := scanMySQLExpense(rows)
	if err != nil {
		return Expense{}, err
	}
	withTags, err := s.attachTags([]Expense{expense})
	if err != nil {
		return Expense{}, err
	}
	return withTags[0], nil
}

func scanMySQLExpense(scanner interface{ Scan(...any) error }) (Expense, error) {
	var e Expense
	var recurring sql.NullString
	if err := scanner.Scan(&e.ID, &recurring, &e.Name, &e.Category, &e.Amount, &e.Currency, &e.Date); err != nil {
		return Expense{}, err
	}
	if recurring.Valid {
		e.RecurringID = recurring.String
	}
	return e, nil
}

func (s *mysqlStore) attachTags(expenses []Expense) ([]Expense, error) {
	if len(expenses) == 0 {
		return expenses, nil
	}
	ids := make([]any, 0, len(expenses))
	index := make(map[string]int, len(expenses))
	for i := range expenses {
		ids = append(ids, expenses[i].ID)
		index[expenses[i].ID] = i
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.Query(
		`SELECT et.expense_id, t.name FROM expense_tags et JOIN tags t ON t.id = et.tag_id
		 WHERE et.expense_id IN (`+placeholders+`) ORDER BY t.name`, ids...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var expenseID, tag string
		if err := rows.Scan(&expenseID, &tag); err != nil {
			return nil, err
		}
		if i, ok := index[expenseID]; ok {
			expenses[i].Tags = append(expenses[i].Tags, tag)
		}
	}
	return expenses, nil
}

func (s *mysqlStore) AddExpense(expense Expense) error {
	return s.insertExpenses([]Expense{expense})
}

func (s *mysqlStore) AddMultipleExpenses(expenses []Expense) error {
	return s.insertExpenses(expenses)
}

func (s *mysqlStore) insertExpenses(expenses []Expense) error {
	if len(expenses) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range expenses {
		if err := s.insertExpenseTx(tx, &expenses[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) insertExpenseTx(tx *sql.Tx, expense *Expense) error {
	if expense.ID == "" {
		expense.ID = uuid.New().String()
	}
	if expense.Currency == "" {
		expense.Currency = s.defaults["currency"]
	}
	if expense.Date.IsZero() {
		expense.Date = time.Now().UTC()
	}
	categoryID, err := resolveCategoryTx(tx, expense.Category)
	if err != nil {
		return err
	}
	var recurring any
	if expense.RecurringID != "" {
		recurring = expense.RecurringID
	}
	if _, err := tx.Exec(`INSERT INTO expenses
		(id, recurring_id, name, category_id, amount, currency, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		expense.ID, recurring, expense.Name, categoryID, expense.Amount, expense.Currency, expense.Date.UTC()); err != nil {
		return fmt.Errorf("failed to insert expense: %v", err)
	}
	return setTagsTx(tx, expense.ID, expense.Tags)
}

func resolveCategoryTx(tx *sql.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM categories WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM categories`).Scan(&next); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO categories (name, sort_order) VALUES (?, ?)`, name, next)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func setTagsTx(tx *sql.Tx, expenseID string, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM expense_tags WHERE expense_id = ?`, expenseID); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		res, err := tx.Exec(`INSERT INTO tags (name) VALUES (?) ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`, tag)
		if err != nil {
			return err
		}
		tagID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT IGNORE INTO expense_tags (expense_id, tag_id) VALUES (?, ?)`, expenseID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func (s *mysqlStore) UpdateExpense(id string, expense Expense) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldRecurring sql.NullString
	var oldCurrency string
	var oldDate time.Time
	if err := tx.QueryRow(`SELECT recurring_id, currency, occurred_at FROM expenses WHERE id = ?`, id).
		Scan(&oldRecurring, &oldCurrency, &oldDate); err != nil {
		return err
	}
	if expense.RecurringID == "" && oldRecurring.Valid {
		expense.RecurringID = oldRecurring.String
	}
	if expense.Currency == "" {
		expense.Currency = oldCurrency
	}
	if expense.Date.IsZero() {
		expense.Date = oldDate
	}
	categoryID, err := resolveCategoryTx(tx, expense.Category)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE expenses SET name = ?, category_id = ?, amount = ?, currency = ?,
		occurred_at = ?, recurring_id = ? WHERE id = ?`,
		expense.Name, categoryID, expense.Amount, expense.Currency, expense.Date.UTC(), nullIfEmpty(expense.RecurringID), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("expense with ID %s not found", id)
	}
	if err := setTagsTx(tx, id, expense.Tags); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *mysqlStore) RemoveExpense(id string) error {
	res, err := s.db.Exec(`DELETE FROM expenses WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("expense with ID %s not found", id)
	}
	return nil
}

func (s *mysqlStore) RemoveMultipleExpenses(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	_, err := s.db.Exec(`DELETE FROM expenses WHERE id IN (`+placeholders+`)`, args...)
	return err
}

func (s *mysqlStore) GetRecurringExpenses() ([]RecurringExpense, error) {
	rows, err := s.db.Query(`SELECT r.id, r.name, r.amount, r.currency, c.name, r.start_date,
		r.repeat_interval, r.occurrences FROM recurring_expenses r
		JOIN categories c ON c.id = r.category_id ORDER BY r.start_date DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecurringExpense
	for rows.Next() {
		var re RecurringExpense
		if err := rows.Scan(&re.ID, &re.Name, &re.Amount, &re.Currency, &re.Category, &re.StartDate, &re.Interval, &re.Occurrences); err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, rows.Err()
}

func (s *mysqlStore) GetRecurringExpense(id string) (RecurringExpense, error) {
	var re RecurringExpense
	err := s.db.QueryRow(`SELECT r.id, r.name, r.amount, r.currency, c.name, r.start_date,
		r.repeat_interval, r.occurrences FROM recurring_expenses r
		JOIN categories c ON c.id = r.category_id WHERE r.id = ?`, id).
		Scan(&re.ID, &re.Name, &re.Amount, &re.Currency, &re.Category, &re.StartDate, &re.Interval, &re.Occurrences)
	if err != nil {
		return RecurringExpense{}, err
	}
	return re, nil
}

func (s *mysqlStore) AddRecurringExpense(recurringExpense RecurringExpense) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if recurringExpense.ID == "" {
		recurringExpense.ID = uuid.New().String()
	}
	if recurringExpense.Currency == "" {
		recurringExpense.Currency = s.defaults["currency"]
	}
	categoryID, err := resolveCategoryTx(tx, recurringExpense.Category)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO recurring_expenses
		(id, name, category_id, amount, currency, repeat_interval, occurrences, start_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		recurringExpense.ID, recurringExpense.Name, categoryID, recurringExpense.Amount,
		recurringExpense.Currency, recurringExpense.Interval, recurringExpense.Occurrences, recurringExpense.StartDate.UTC()); err != nil {
		return err
	}
	for _, expense := range generateExpensesFromRecurring(recurringExpense, false) {
		if err := s.insertExpenseTx(tx, &expense); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) UpdateRecurringExpense(id string, recurringExpense RecurringExpense, updateAll bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	recurringExpense.ID = id
	if recurringExpense.Currency == "" {
		recurringExpense.Currency = s.defaults["currency"]
	}
	categoryID, err := resolveCategoryTx(tx, recurringExpense.Category)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE recurring_expenses SET name = ?, category_id = ?, amount = ?, currency = ?,
		repeat_interval = ?, occurrences = ?, start_date = ? WHERE id = ?`,
		recurringExpense.Name, categoryID, recurringExpense.Amount, recurringExpense.Currency,
		recurringExpense.Interval, recurringExpense.Occurrences, recurringExpense.StartDate.UTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("recurring expense with ID %s not found", id)
	}
	if updateAll {
		if _, err := tx.Exec(`DELETE FROM expenses WHERE recurring_id = ?`, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM expenses WHERE recurring_id = ? AND occurred_at > ?`, id, time.Now().UTC()); err != nil {
			return err
		}
	}
	for _, expense := range generateExpensesFromRecurring(recurringExpense, !updateAll) {
		if err := s.insertExpenseTx(tx, &expense); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *mysqlStore) RemoveRecurringExpense(id string, removeAll bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if removeAll {
		if _, err := tx.Exec(`DELETE FROM expenses WHERE recurring_id = ?`, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM expenses WHERE recurring_id = ? AND occurred_at > ?`, id, time.Now().UTC()); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`DELETE FROM recurring_expenses WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("recurring expense with ID %s not found", id)
	}
	return tx.Commit()
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
