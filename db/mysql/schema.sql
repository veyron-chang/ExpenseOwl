-- ExpenseOwl MySQL 8 schema.
-- Run against a MySQL 8 server. The database/user creation is intentionally
-- left out of this file because credentials must not be committed.

CREATE DATABASE IF NOT EXISTS expenseowl
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;

USE expenseowl;

CREATE TABLE IF NOT EXISTS app_config (
  id TINYINT NOT NULL DEFAULT 1,
  currency CHAR(3) NOT NULL,
  start_date TINYINT UNSIGNED NOT NULL,
  locale VARCHAR(10) NOT NULL DEFAULT 'zh-CN',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  CONSTRAINT chk_config_single CHECK (id = 1),
  CONSTRAINT chk_config_start_date CHECK (start_date BETWEEN 1 AND 31)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(64) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uq_categories_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS recurring_expenses (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS expenses (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS tags (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(64) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_tags_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS expense_tags (
  expense_id CHAR(36) NOT NULL,
  tag_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (expense_id, tag_id),
  KEY idx_expense_tags_tag (tag_id),
  CONSTRAINT fk_expense_tags_expense FOREIGN KEY (expense_id) REFERENCES expenses(id) ON DELETE CASCADE,
  CONSTRAINT fk_expense_tags_tag FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS import_batches (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS import_rows (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(32) NOT NULL,
  applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO app_config (id, currency, start_date)
VALUES (1, 'usd', 1)
ON DUPLICATE KEY UPDATE id = id;

INSERT IGNORE INTO categories (name, sort_order) VALUES
  ('Food', 0),
  ('Groceries', 1),
  ('Travel', 2),
  ('Rent', 3),
  ('Utilities', 4),
  ('Entertainment', 5),
  ('Healthcare', 6),
  ('Shopping', 7),
  ('Miscellaneous', 8),
  ('Income', 9);

INSERT IGNORE INTO schema_migrations (version) VALUES ('001_init');
