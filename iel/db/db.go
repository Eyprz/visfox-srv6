// Package db embeds the SQLite schema of the IEL.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

// Schema creates every table if missing (idempotent).
//
//go:embed schema.sql
var Schema string

// Version is the schema version (PRAGMA user_version). Raise it when a table changes: the IEL
// refuses a database of another version (v1 has no migrations; recreate the development database).
const Version = 2

// Apply checks the schema version of the database and creates the missing tables.
func Apply(ctx context.Context, conn *sql.DB) error {
	var v int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	var tables int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 && v != Version {
		return fmt.Errorf("database schema version %d, this IEL needs %d: delete the database file to recreate it", v, Version)
	}
	if _, err := conn.ExecContext(ctx, Schema); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, Version))
	return err
}
