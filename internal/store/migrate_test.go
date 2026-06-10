package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMigrationFilename(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantVer   int
		wantDir   string
		wantError bool
	}{
		{"valid up", "001_legacy_service_state.up.sql", 1, "up", false},
		{"valid down", "002_domain_projection_tables.down.sql", 2, "down", false},
		{"no direction", "001_migration.sql", 0, "", true},
		{"no underscore", "001.sql", 0, "", true},
		{"invalid version", "abc_migration.up.sql", 0, "", true},
		{"not sql", "001_migration.up.txt", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, dir, err := parseMigrationFilename(tt.input)
			if tt.wantError {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ver != tt.wantVer {
				t.Errorf("version: got %d, want %d", ver, tt.wantVer)
			}
			if dir != tt.wantDir {
				t.Errorf("direction: got %q, want %q", dir, tt.wantDir)
			}
		})
	}
}

func TestMigrationManagerLoadMigrations(t *testing.T) {
	dir := t.TempDir()

	// Write test migration files.
	os.WriteFile(filepath.Join(dir, "001_test.up.sql"), []byte("CREATE TABLE test (id int);"), 0o644)
	os.WriteFile(filepath.Join(dir, "001_test.down.sql"), []byte("DROP TABLE test;"), 0o644)
	os.WriteFile(filepath.Join(dir, "002_other.up.sql"), []byte("CREATE TABLE other (id int);"), 0o644)

	mgr := &MigrationManager{db: nil, migrationsDir: dir}
	migrations, err := mgr.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if len(migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrations))
	}
	if migrations[0].Version != 1 {
		t.Errorf("expected version 1, got %d", migrations[0].Version)
	}
	if migrations[0].Up != "CREATE TABLE test (id int);" {
		t.Errorf("unexpected up SQL: %s", migrations[0].Up)
	}
	if migrations[0].Down != "DROP TABLE test;" {
		t.Errorf("unexpected down SQL: %s", migrations[0].Down)
	}
	if migrations[1].Version != 2 {
		t.Errorf("expected version 2, got %d", migrations[1].Version)
	}
}

func TestMigrationManagerLoadMigrationsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	mgr := &MigrationManager{db: nil, migrationsDir: dir}
	migrations, err := mgr.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 0 {
		t.Errorf("expected 0 migrations, got %d", len(migrations))
	}
}

func TestMigrationManagerLoadMigrationsNonSQL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a migration"), 0o644)
	os.WriteFile(filepath.Join(dir, "001_test.up.sql"), []byte("SELECT 1;"), 0o644)

	mgr := &MigrationManager{db: nil, migrationsDir: dir}
	migrations, err := mgr.LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 {
		t.Errorf("expected 1 migration, got %d", len(migrations))
	}
}
