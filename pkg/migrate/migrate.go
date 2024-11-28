// Package migrate applies embedded, forward-only SQL migrations.
//
// Files are applied in lexical order (001_x.sql, 002_y.sql, ...). Each file
// runs in its own transaction and is recorded in schema_migrations, so a
// restart only applies what is new. A Postgres advisory lock serialises
// concurrent replicas starting at the same time.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

// lockID is an arbitrary constant shared by every replica of a service.
const lockID = 7_310_455_102

// Up applies every migration in fsys that is not yet recorded.
// It returns the names of the files it applied.
func Up(ctx context.Context, db *sqlx.DB, fsys fs.FS) ([]string, error) {
	conn, err := db.Connx(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID) //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	var done []string
	if err := conn.SelectContext(ctx, &done, `SELECT name FROM schema_migrations`); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	applied := make(map[string]bool, len(done))
	for _, n := range done {
		applied[n] = true
	}

	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	var ran []string
	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return ran, fmt.Errorf("read %s: %w", name, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			continue
		}

		tx, err := conn.BeginTxx(ctx, nil)
		if err != nil {
			return ran, fmt.Errorf("begin %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return ran, fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback()
			return ran, fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return ran, fmt.Errorf("commit %s: %w", name, err)
		}
		ran = append(ran, name)
	}
	return ran, nil
}
