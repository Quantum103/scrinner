package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB предоставляет доступ к SQLite-базе и управляет её миграциями.
type DB struct {
	db   *sql.DB
	path string
}

// Open открывает SQLite-базу, создаёт каталог файла и применяет основные настройки соединения.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("путь к базе данных не задан")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("создать каталог базы данных: %w", err)
	}

	dsn := "file:" + filepath.ToSlash(path) +
		"?_busy_timeout=5000" +
		"&_foreign_keys=on" +
		"&_journal_mode=WAL" +
		"&_synchronous=NORMAL"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite: %w", err)
	}

	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("проверить соединение SQLite: %w", err)
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("определить абсолютный путь SQLite: %w", err)
	}
	return &DB{db: db, path: absolutePath}, nil
}

// Close закрывает пул соединений с SQLite.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}

	return d.db.Close()
}

// SQLDB возвращает низкоуровневый пул database/sql для внутренних операций и тестов.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// Migrate последовательно применяет ещё не выполненные SQL-миграции.
func (d *DB) Migrate(ctx context.Context, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("прочитать каталог миграций: %w", err)
	}

	type migration struct {
		name    string
		version int64
		path    string
	}

	migrations := make([]migration, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) != 2 {
			return fmt.Errorf("некорректное имя миграции %q", entry.Name())
		}

		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || version <= 0 {
			return fmt.Errorf(
				"некорректная версия миграции %q",
				entry.Name(),
			)
		}

		migrations = append(
			migrations,
			migration{
				name:    entry.Name(),
				version: version,
				path:    filepath.Join(migrationsDir, entry.Name()),
			},
		)
	}

	sort.Slice(
		migrations,
		func(i, j int) bool {
			return migrations[i].version < migrations[j].version
		},
	)

	if _, err := d.db.ExecContext(
		ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
	); err != nil {
		return fmt.Errorf("создать таблицу миграций: %w", err)
	}

	for _, migration := range migrations {
		var applied int

		if err := d.db.QueryRowContext(
			ctx,
			`SELECT COUNT(1)
			 FROM schema_migrations
			 WHERE version = ?`,
			migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf(
				"проверить миграцию %d: %w",
				migration.version,
				err,
			)
		}

		if applied > 0 {
			continue
		}

		data, err := os.ReadFile(migration.path)
		if err != nil {
			return fmt.Errorf(
				"прочитать миграцию %q: %w",
				migration.name,
				err,
			)
		}

		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf(
				"начать транзакцию миграции %d: %w",
				migration.version,
				err,
			)
		}

		if _, err = tx.ExecContext(ctx, string(data)); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf(
				"применить миграцию %d: %w",
				migration.version,
				err,
			)
		}

		if _, err = tx.ExecContext(
			ctx,
			`INSERT INTO schema_migrations(version, applied_at)
			 VALUES (?, ?)`,
			migration.version,
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf(
				"зафиксировать миграцию %d: %w",
				migration.version,
				err,
			)
		}

		if err = tx.Commit(); err != nil {
			return fmt.Errorf(
				"зафиксировать транзакцию миграции %d: %w",
				migration.version,
				err,
			)
		}
	}

	return nil
}

func utcText(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"разобрать UTC timestamp %q: %w",
			value,
			err,
		)
	}

	return t.UTC(), nil
}
