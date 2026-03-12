package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNoDatabase
	}

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	if _, err := db.ExecContext(ctx, schemaMigrations[0].Statements[0]); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	currentVersion, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}

	for _, migration := range schemaMigrations {
		if migration.Version <= currentVersion {
			continue
		}
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}

	return nil
}

func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version sql.NullInt64
	if err := db.QueryRowContext(
		ctx,
		`SELECT MAX(version) FROM schema_migrations;`,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("query current schema version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d %s: %w", migration.Version, migration.Name, err)
	}

	for _, statement := range migration.Statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf(
				"apply migration %d %s: %w",
				migration.Version,
				migration.Name,
				err,
			)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)
		 ON CONFLICT(version) DO NOTHING;`,
		migration.Version,
		migration.Name,
		formatTime(time.Now()),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %d %s: %w", migration.Version, migration.Name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d %s: %w", migration.Version, migration.Name, err)
	}

	return nil
}
