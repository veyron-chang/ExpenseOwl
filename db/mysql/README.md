# MySQL 8 setup

## Create the database and application user

Run as a MySQL administrator:

```sql
CREATE DATABASE IF NOT EXISTS expenseowl
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;

CREATE USER 'expenseowl'@'localhost'
  IDENTIFIED BY 'replace-with-a-strong-password';

GRANT ALL PRIVILEGES ON expenseowl.* TO 'expenseowl'@'localhost';
FLUSH PRIVILEGES;
```

## Initialize tables

The application creates missing tables on startup. For a manual setup, run:

```bash
mysql -u expenseowl -p expenseowl < schema.sql
```

`schema.sql` also contains `CREATE DATABASE`/`USE`; if the application user
does not have global `CREATE` privilege, run it as root or remove those two
statements and use the already-created `expenseowl` database.

## Environment

The application reads:

```env
STORAGE_TYPE=mysql
MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=expenseowl
MYSQL_USER=expenseowl
MYSQL_PASSWORD=...
```

`MYSQL_HOST`/`MYSQL_PORT`/`MYSQL_DATABASE` are combined into the Go driver DSN;
`STORAGE_URL`/`STORAGE_USER`/`STORAGE_PASS` remain supported for compatibility.

Do not commit real credentials. The local development credentials are kept in
`.secrets/mysql.env` at the assignment workspace root, outside this repository.

## Schema notes

- `app_config` is a single-row table (`CHECK id = 1`).
- `categories` and `tags` are normalized; `expense_tags` is the many-to-many
  table.
- `expenses.category_id` and `expenses.recurring_id` have foreign keys.
- Amounts use `DECIMAL(12,2)`; timestamps use `DATETIME(6)` in UTC.
- `import_batches`/`import_rows` are reserved for the import audit flow.
- `schema_migrations` records the initial schema version.
