package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const postgresServiceStateKey = "service-state"

type PostgresMigration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

type AppliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

var postgresMigrations = withMigrationChecksums([]PostgresMigration{
	{
		Version: 1,
		Name:    "legacy_service_state",
		SQL: `
CREATE TABLE IF NOT EXISTS service_state (
  key text PRIMARY KEY,
  payload jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
)`,
	},
	{
		Version: 2,
		Name:    "domain_projection_tables",
		SQL: `
CREATE TABLE IF NOT EXISTS projects (
  id text PRIMARY KEY,
  created_at timestamptz,
  updated_at timestamptz,
  payload jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS requirements (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  position int NOT NULL,
  created_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS requirements_project_position_idx ON requirements(project_id, position);
CREATE TABLE IF NOT EXISTS plans (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  version int NOT NULL,
  created_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS plans_project_version_idx ON plans(project_id, version);
CREATE TABLE IF NOT EXISTS contracts (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  version int NOT NULL,
  created_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS contracts_project_version_idx ON contracts(project_id, version);
CREATE TABLE IF NOT EXISTS tasks (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  status text NOT NULL,
  assignee_agent text,
  created_at timestamptz,
  updated_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_project_status_idx ON tasks(project_id, status);
CREATE TABLE IF NOT EXISTS task_order (
  project_id text NOT NULL,
  position int NOT NULL,
  task_id text NOT NULL,
  PRIMARY KEY(project_id, position)
);
CREATE TABLE IF NOT EXISTS agent_runs (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  task_id text NOT NULL,
  status text NOT NULL,
  created_at timestamptz,
  updated_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS agent_runs_project_status_idx ON agent_runs(project_id, status);
CREATE TABLE IF NOT EXISTS run_order (
  project_id text NOT NULL,
  position int NOT NULL,
  run_id text NOT NULL,
  PRIMARY KEY(project_id, position)
);
CREATE TABLE IF NOT EXISTS artifacts (
  id text PRIMARY KEY,
  project_id text NOT NULL,
  run_id text,
  created_at timestamptz,
  payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS artifacts_project_idx ON artifacts(project_id);
CREATE TABLE IF NOT EXISTS artifact_order (
  project_id text NOT NULL,
  position int NOT NULL,
  artifact_id text NOT NULL,
  PRIMARY KEY(project_id, position)
)`,
	},
})

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
	return s.Migrate(ctx)
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.initErr != nil {
		return s.initErr
	}
	if s.db == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  checksum text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return err
	}
	for _, migration := range postgresMigrations {
		var applied AppliedMigration
		err := tx.QueryRowContext(ctx, `SELECT version, name, checksum FROM schema_migrations WHERE version = $1`, migration.Version).Scan(&applied.Version, &applied.Name, &applied.Checksum)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if applied.Name != migration.Name || applied.Checksum != migration.Checksum {
				return fmt.Errorf("postgres migration %d checksum mismatch", migration.Version)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply postgres migration %d %s: %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`, migration.Version, migration.Name, migration.Checksum); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) AppliedMigrations(ctx context.Context) ([]AppliedMigration, error) {
	if s == nil {
		return nil, nil
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	if s.db == nil {
		return nil, nil
	}
	if err := s.Migrate(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applied []AppliedMigration
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.Checksum); err != nil {
			return nil, err
		}
		applied = append(applied, migration)
	}
	return applied, rows.Err()
}

func (s *PostgresStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func PostgresMigrations() []PostgresMigration {
	migrations := make([]PostgresMigration, len(postgresMigrations))
	copy(migrations, postgresMigrations)
	return migrations
}

func PostgresServiceStateKey() string {
	return postgresServiceStateKey
}

func withMigrationChecksums(migrations []PostgresMigration) []PostgresMigration {
	for i := range migrations {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", migrations[i].Version, migrations[i].Name, migrations[i].SQL)))
		migrations[i].Checksum = hex.EncodeToString(sum[:])
	}
	return migrations
}

func CheckPostgres(ctx context.Context, dsn string) error {
	store := NewPostgresStore(dsn)
	if store == nil || store.db == nil {
		return fmt.Errorf("postgres store is not configured")
	}
	if err := store.db.PingContext(ctx); err != nil {
		return err
	}
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	if err := store.checkProjectionTables(ctx); err != nil {
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

func (s *PostgresStore) checkProjectionTables(ctx context.Context) error {
	for _, table := range []string{"projects", "requirements", "plans", "contracts", "tasks", "task_order", "agent_runs", "run_order", "artifacts", "artifact_order"} {
		query := fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", table)
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("check postgres projection table %s: %w", table, err)
		}
	}
	return nil
}
