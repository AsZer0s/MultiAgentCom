package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreLoadMissingState(t *testing.T) {
	payload, err := NewFileStore(t.TempDir()).Load(context.Background())
	if err != nil {
		t.Fatalf("load missing state: %v", err)
	}
	if payload != nil {
		t.Fatalf("expected nil payload for missing state, got %q", payload)
	}
}

func TestFileStoreSaveCreatesStateFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "data")
	state := []byte(`{"version":1}`)

	if err := NewFileStore(root).Save(context.Background(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	payload, err := os.ReadFile(ServiceStatePath(root))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if string(payload) != string(state) {
		t.Fatalf("state file = %q, want %q", payload, state)
	}
}

func TestFileStoreSaveRejectsFileDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data-root")
	if err := os.WriteFile(root, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file data root: %v", err)
	}

	err := NewFileStore(root).Save(context.Background(), []byte(`{"version":1}`))
	if err == nil {
		t.Fatal("expected save to fail when data root is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") && !strings.Contains(err.Error(), "not a dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceStatePath(t *testing.T) {
	root := t.TempDir()
	if got := ServiceStatePath(root); got != filepath.Join(root, ServiceStateFilename) {
		t.Fatalf("ServiceStatePath = %q", got)
	}
}

func TestPostgresMigrationsAreIdempotent(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	applied, err := store.AppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	expected := PostgresMigrations()
	if len(applied) != len(expected) {
		t.Fatalf("applied migrations = %d, want %d", len(applied), len(expected))
	}
	for i := range expected {
		if applied[i].Version != expected[i].Version || applied[i].Name != expected[i].Name || applied[i].Checksum != expected[i].Checksum {
			t.Fatalf("migration[%d] = %+v, want %+v", i, applied[i], expected[i])
		}
	}
}

func TestPostgresMigrateRejectsChecksumMismatch(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	migrations := PostgresMigrations()
	if _, err := store.db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = $1`, migrations[0].Version); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = $1 WHERE version = $2`, migrations[0].Checksum, migrations[0].Version)
	})
	if err := store.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestPostgresStoreLoadMissingState(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if store == nil {
		t.Fatal("expected postgres store")
	}
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM service_state WHERE key = $1`, postgresServiceStateKey); err != nil {
		t.Fatalf("clear state: %v", err)
	}
	payload, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("load missing state: %v", err)
	}
	if payload != nil {
		t.Fatalf("expected nil payload, got %q", payload)
	}
}

func TestPostgresStoreSaveAndLoadState(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	state := []byte(`{"version":1,"projects":{"p1":{"id":"p1","name":"demo"}}}`)
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	payload, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode loaded state: %v", err)
	}
	if decoded["version"].(float64) != 1 {
		t.Fatalf("unexpected version: %v", decoded["version"])
	}
	if _, ok := decoded["projects"].(map[string]any)["p1"]; !ok {
		t.Fatalf("expected project p1 in loaded state: %v", decoded)
	}
}

func TestCheckPostgresAcceptsFreshMigratedDatabase(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"artifact_order", "artifacts", "run_order", "agent_runs", "task_order", "tasks", "contracts", "plans", "requirements", "projects", "service_state"} {
		if _, err := store.db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	if err := CheckPostgres(ctx, dsn); err != nil {
		t.Fatalf("check fresh postgres: %v", err)
	}
}

func TestCheckPostgresRejectsUnsupportedStateVersion(t *testing.T) {
	dsn := os.Getenv("MULTI_AGENT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("MULTI_AGENT_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	store := NewPostgresStore(dsn)
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM service_state WHERE key = $1`, postgresServiceStateKey); err != nil {
		t.Fatalf("clear state: %v", err)
	}
	if err := store.Save(ctx, []byte(`{"version":2}`)); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := CheckPostgres(ctx, dsn); err == nil || !strings.Contains(err.Error(), "unsupported service state version") {
		t.Fatalf("expected unsupported version error, got %v", err)
	}
}
