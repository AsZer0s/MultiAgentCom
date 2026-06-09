package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"multiagentcom/internal/store"
)

const (
	advisoryLockProjectSeed = 54321
	advisoryLockTaskSeed    = 12345
)

// AdvisoryLockGuard provides project-scoped and task-scoped Postgres advisory locks
// to prevent concurrent mutation of the same entity.
type AdvisoryLockGuard struct {
	raw *store.PostgresStore
}

// newAdvisoryLockGuard creates a concurrency guard backed by Postgres advisory locks.
func newAdvisoryLockGuard(ext *postgresExtendedRepository) *AdvisoryLockGuard {
	if ext == nil {
		return nil
	}
	return &AdvisoryLockGuard{raw: ext.raw}
}

// LockProject acquires a session-level advisory lock for the given project.
// The lock is automatically released when the session/transaction ends.
func (g *AdvisoryLockGuard) LockProject(ctx context.Context, projectID string) error {
	if g.raw == nil || g.raw.DB() == nil {
		return nil // no-op for non-postgres backends
	}
	lockID := advisoryLockID(advisoryLockProjectSeed, projectID)
	_, err := g.raw.DB().ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`, int64(lockID>>32), int64(lockID&0xFFFFFFFF))
	if err != nil {
		return fmt.Errorf("acquire project advisory lock for %s: %w", projectID, err)
	}
	return nil
}

// UnlockProject releases the session-level advisory lock for the given project.
func (g *AdvisoryLockGuard) UnlockProject(ctx context.Context, projectID string) error {
	if g.raw == nil || g.raw.DB() == nil {
		return nil
	}
	lockID := advisoryLockID(advisoryLockProjectSeed, projectID)
	_, err := g.raw.DB().ExecContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, int64(lockID>>32), int64(lockID&0xFFFFFFFF))
	if err != nil {
		return fmt.Errorf("release project advisory lock for %s: %w", projectID, err)
	}
	return nil
}

// TryLockProject attempts to acquire a project advisory lock without blocking.
// Returns true if the lock was acquired, false if it was already held.
func (g *AdvisoryLockGuard) TryLockProject(ctx context.Context, projectID string) (bool, error) {
	if g.raw == nil || g.raw.DB() == nil {
		return true, nil
	}
	lockID := advisoryLockID(advisoryLockProjectSeed, projectID)
	var acquired bool
	err := g.raw.DB().QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, int64(lockID>>32), int64(lockID&0xFFFFFFFF)).Scan(&acquired)
	if err != nil {
		return false, fmt.Errorf("try project advisory lock for %s: %w", projectID, err)
	}
	return acquired, nil
}

// LockTask acquires a session-level advisory lock for the given task.
func (g *AdvisoryLockGuard) LockTask(ctx context.Context, taskID string) error {
	if g.raw == nil || g.raw.DB() == nil {
		return nil
	}
	lockID := advisoryLockID(advisoryLockTaskSeed, taskID)
	_, err := g.raw.DB().ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`, int64(lockID>>32), int64(lockID&0xFFFFFFFF))
	if err != nil {
		return fmt.Errorf("acquire task advisory lock for %s: %w", taskID, err)
	}
	return nil
}

// UnlockTask releases the session-level advisory lock for the given task.
func (g *AdvisoryLockGuard) UnlockTask(ctx context.Context, taskID string) error {
	if g.raw == nil || g.raw.DB() == nil {
		return nil
	}
	lockID := advisoryLockID(advisoryLockTaskSeed, taskID)
	_, err := g.raw.DB().ExecContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, int64(lockID>>32), int64(lockID&0xFFFFFFFF))
	if err != nil {
		return fmt.Errorf("release task advisory lock for %s: %w", taskID, err)
	}
	return nil
}

// advisoryLockID generates a deterministic 64-bit lock ID from a seed and an entity ID string.
// It uses a simple hash to avoid collisions between different entity types.
func advisoryLockID(seed int, id string) uint64 {
	// Simple FNV-1a-like hash for deterministic lock IDs
	h := uint64(seed)
	for _, c := range id {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// IdempotencyKey represents a deduplication key for write operations.
type IdempotencyKey struct {
	ProjectID string
	Operation string
	Key       string
}

// String returns the idempotency key in a structured format.
func (k IdempotencyKey) String() string {
	return fmt.Sprintf("%s:%s:%s", k.ProjectID, k.Operation, k.Key)
}

// CheckIdempotency verifies whether an operation with the given key has already been applied.
// For the Postgres backend, it uses a dedicated idempotency_keys table.
// For memory/file backends, it is a no-op.
func (g *AdvisoryLockGuard) CheckIdempotency(ctx context.Context, key IdempotencyKey) (bool, error) {
	if g.raw == nil || g.raw.DB() == nil {
		return false, nil
	}
	// Ensure the table exists
	if _, err := g.raw.DB().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS idempotency_keys (
			key_text text PRIMARY KEY,
			created_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return false, err
	}
	var exists string
	err := g.raw.DB().QueryRowContext(ctx, `SELECT key_text FROM idempotency_keys WHERE key_text = $1`, key.String()).Scan(&exists)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecordIdempotency marks an operation as completed to prevent duplicates.
func (g *AdvisoryLockGuard) RecordIdempotency(ctx context.Context, key IdempotencyKey) error {
	if g.raw == nil || g.raw.DB() == nil {
		return nil
	}
	_, err := g.raw.DB().ExecContext(ctx, `
		INSERT INTO idempotency_keys (key_text, created_at) VALUES ($1, now())
		ON CONFLICT (key_text) DO NOTHING`, key.String())
	return err
}

// ParseIdempotencyKey parses an idempotency key from an HTTP header value.
// Format: "projectID:operation:key"
func ParseIdempotencyKey(header string) (IdempotencyKey, error) {
	parts := strings.SplitN(header, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return IdempotencyKey{}, fmt.Errorf("invalid idempotency key format, expected projectID:operation:key")
	}
	return IdempotencyKey{ProjectID: parts[0], Operation: parts[1], Key: parts[2]}, nil
}

// acquireAdvisoryProjectLock is a helper that acquires the project lock only for Postgres backends.
func (s *Service) acquireAdvisoryProjectLock(ctx context.Context, projectID string) error {
	if s.lockGuard == nil {
		return nil
	}
	return s.lockGuard.LockProject(ctx, projectID)
}

// releaseAdvisoryProjectLock is a helper that releases the project lock only for Postgres backends.
func (s *Service) releaseAdvisoryProjectLock(ctx context.Context, projectID string) {
	if s.lockGuard == nil {
		return
	}
	_ = s.lockGuard.UnlockProject(ctx, projectID)
}

// checkIdempotencyKey checks and records an idempotency key for the given operation.
func (s *Service) checkIdempotencyKey(ctx context.Context, key IdempotencyKey) (bool, error) {
	if s.lockGuard == nil {
		return false, nil
	}
	alreadyApplied, err := s.lockGuard.CheckIdempotency(ctx, key)
	if err != nil {
		return false, err
	}
	if alreadyApplied {
		return true, nil
	}
	return false, s.lockGuard.RecordIdempotency(ctx, key)
}

// strconv import is used by ParseIdempotencyKey - ensure it's available
var _ = strconv.Itoa
