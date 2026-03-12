package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrNoDatabase = errors.New("sqlite: no database connection")

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Open(driver, path string) (*sql.DB, error) {
	if path != "" && path != ":memory:" && path != "file::memory:?cache=shared" {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite directory %s: %w", dir, err)
			}
		}
	}
	db, err := sql.Open(driver, path)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func Ping(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNoDatabase
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	return nil
}
