package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const postgresServiceStateKey = "service-state"

const postgresServiceStateTable = `
CREATE TABLE IF NOT EXISTS service_state (
  key text PRIMARY KEY,
  payload jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
)`

type PostgresStore struct {
	db      *sql.DB
	initErr error
}

func NewPostgresStore(dsn string) *PostgresStore {
	db, err := sql.Open("pgx", strings.TrimSpace(dsn))
	if err != nil {
		return &PostgresStore{initErr: err}
	}
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Load(ctx context.Context) ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	if s.db == nil {
		return nil, nil
	}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM service_state WHERE key = $1`, postgresServiceStateKey).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return payload, nil
}

func (s *PostgresStore) Save(ctx context.Context, payload []byte) error {
	if s == nil {
		return nil
	}
	if s.initErr != nil {
		return s.initErr
	}
	if s.db == nil {
		return nil
	}
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO service_state (key, payload, updated_at)
VALUES ($1, $2::jsonb, now())
ON CONFLICT (key) DO UPDATE SET payload = EXCLUDED.payload, updated_at = now()
`, postgresServiceStateKey, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, postgresServiceStateTable)
	return err
}

func CheckPostgres(ctx context.Context, dsn string) error {
	store := NewPostgresStore(dsn)
	if store == nil || store.db == nil {
		return fmt.Errorf("postgres store is not configured")
	}
	if err := store.db.PingContext(ctx); err != nil {
		return err
	}
	if err := store.ensureSchema(ctx); err != nil {
		return err
	}
	payload, err := store.Load(ctx)
	if err != nil || len(payload) == 0 {
		return err
	}
	var state struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return fmt.Errorf("decode service state: %w", err)
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported service state version %d", state.Version)
	}
	return nil
}
