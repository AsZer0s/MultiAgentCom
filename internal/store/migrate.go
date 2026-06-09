package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Migration represents a single database migration with up and down SQL.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// MigrationManager manages database migrations from SQL files on disk.
type MigrationManager struct {
	db          *sql.DB
	migrationsDir string
}

// NewMigrationManager creates a migration manager that reads from the given directory.
func NewMigrationManager(db *sql.DB, migrationsDir string) *MigrationManager {
	return &MigrationManager{
		db:            db,
		migrationsDir: migrationsDir,
	}
}

// LoadMigrations reads all .sql migration files from the migrations directory.
func (m *MigrationManager) LoadMigrations() ([]Migration, error) {
	entries, err := os.ReadDir(m.migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	migrationMap := make(map[int]*Migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, direction, err := parseMigrationFilename(name)
		if err != nil {
			continue
		}

		data, err := os.ReadFile(filepath.Join(m.migrationsDir, name))
		if err != nil {
			return nil, fmt.Errorf("read migration file %s: %w", name, err)
		}

		mig, ok := migrationMap[version]
		if !ok {
			mig = &Migration{Version: version, Name: strings.TrimSuffix(strings.TrimSuffix(name, ".up.sql"), ".down.sql")}
			migrationMap[version] = mig
		}

		if direction == "up" {
			mig.Up = string(data)
		} else {
			mig.Down = string(data)
		}
	}

	migrations := make([]Migration, 0, len(migrationMap))
	for _, mig := range migrationMap {
		migrations = append(migrations, *mig)
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// parseMigrationFilename extracts version and direction from filenames like "001_name.up.sql".
func parseMigrationFilename(name string) (int, string, error) {
	var version int
	var direction string

	if strings.HasSuffix(name, ".up.sql") {
		direction = "up"
		name = strings.TrimSuffix(name, ".up.sql")
	} else if strings.HasSuffix(name, ".down.sql") {
		direction = "down"
		name = strings.TrimSuffix(name, ".down.sql")
	} else {
		return 0, "", fmt.Errorf("not a migration file: %s", name)
	}

	parts := strings.SplitN(name, "_", 2)
	if len(parts) < 2 {
		return 0, "", fmt.Errorf("invalid migration filename: %s", name)
	}

	if _, err := fmt.Sscanf(parts[0], "%d", &version); err != nil {
		return 0, "", fmt.Errorf("invalid version in %s: %w", name, err)
	}

	return version, direction, nil
}

// Up runs all pending up migrations.
func (m *MigrationManager) Up(ctx context.Context) error {
	migrations, err := m.LoadMigrations()
	if err != nil {
		return err
	}

	if err := m.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, mig := range migrations {
		if applied[mig.Version] {
			continue
		}
		if mig.Up == "" {
			return fmt.Errorf("migration %d has no up SQL", mig.Version)
		}

		if _, err := m.db.ExecContext(ctx, mig.Up); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", mig.Version, mig.Name, err)
		}

		if _, err := m.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, now())`,
			mig.Version, mig.Name,
		); err != nil {
			return fmt.Errorf("record migration %d: %w", mig.Version, err)
		}
	}

	return nil
}

// Down rolls back the last applied migration.
func (m *MigrationManager) Down(ctx context.Context) error {
	migrations, err := m.LoadMigrations()
	if err != nil {
		return err
	}

	if err := m.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return err
	}

	// Find the highest applied version.
	var lastApplied int
	for v := range applied {
		if v > lastApplied {
			lastApplied = v
		}
	}
	if lastApplied == 0 {
		return fmt.Errorf("no migrations to roll back")
	}

	// Find the migration.
	var target *Migration
	for _, mig := range migrations {
		if mig.Version == lastApplied {
			target = &mig
			break
		}
	}
	if target == nil {
		return fmt.Errorf("migration %d not found", lastApplied)
	}
	if target.Down == "" {
		return fmt.Errorf("migration %d has no down SQL", lastApplied)
	}

	if _, err := m.db.ExecContext(ctx, target.Down); err != nil {
		return fmt.Errorf("rollback migration %d (%s): %w", target.Version, target.Name, err)
	}

	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = $1`, lastApplied,
	); err != nil {
		return fmt.Errorf("remove migration record %d: %w", lastApplied, err)
	}

	return nil
}

// Status returns the list of migrations with their applied status.
func (m *MigrationManager) Status(ctx context.Context) ([]MigrationStatus, error) {
	migrations, err := m.LoadMigrations()
	if err != nil {
		return nil, err
	}

	if err := m.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	var result []MigrationStatus
	for _, mig := range migrations {
		status := MigrationStatus{
			Version: mig.Version,
			Name:    mig.Name,
			Applied: applied[mig.Version],
		}
		result = append(result, status)
	}
	return result, nil
}

// MigrationStatus represents the status of a single migration.
type MigrationStatus struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
}

func (m *MigrationManager) ensureMigrationsTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`)
	return err
}

func (m *MigrationManager) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}
