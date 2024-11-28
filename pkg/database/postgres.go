package database

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // registers the "postgres" driver

	"github.com/ramisoul84/kfc-loyalty/internal/config"
)

// Postgres wraps a sqlx.DB.
type Postgres struct {
	DB *sqlx.DB
}

// NewPostgres connects to PostgreSQL and configures the connection pool.
func NewPostgres(cfg *config.DatabaseConfig, dsn string) (*Postgres, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxConns)
	db.SetMaxIdleConns(cfg.MinConns)
	db.SetConnMaxLifetime(cfg.MaxConnLifetime)
	db.SetConnMaxIdleTime(cfg.MaxConnIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Postgres{DB: db}, nil
}

// Close closes the database connection pool.
func (p *Postgres) Close() error {
	if p.DB != nil {
		return p.DB.Close()
	}
	return nil
}
