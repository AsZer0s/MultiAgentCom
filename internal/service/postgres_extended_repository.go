package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/store"
)

// postgresExtendedRepository handles persistence for context_injections, sandboxes,
// snapshots, audit_logs, and alerts projection tables.
type postgresExtendedRepository struct {
	raw *store.PostgresStore
}

func (r postgresExtendedRepository) beginTx(ctx context.Context) (*sql.Tx, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	return r.raw.DB().BeginTx(ctx, nil)
}

// SaveContextInjections persists a batch of context injections for a project.
func (r postgresExtendedRepository) SaveContextInjections(ctx context.Context, tx *sql.Tx, injections []*domain.ContextInjection) error {
	for _, inj := range injections {
		if inj == nil {
			continue
		}
		payload, err := json.Marshal(inj)
		if err != nil {
			return fmt.Errorf("encode context injection: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO context_injections (id, project_id, task_id, version, created_at, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			inj.ID, inj.ProjectID, inj.TaskID, inj.Version, inj.CreatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveSandboxes persists a batch of sandboxes.
func (r postgresExtendedRepository) SaveSandboxes(ctx context.Context, tx *sql.Tx, sandboxes []*domain.Sandbox) error {
	for _, sb := range sandboxes {
		if sb == nil {
			continue
		}
		payload, err := json.Marshal(sb)
		if err != nil {
			return fmt.Errorf("encode sandbox: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sandboxes (id, project_id, run_id, task_id, status, created_at, updated_at, payload)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, payload = EXCLUDED.payload`,
			sb.ID, sb.ProjectID, sb.RunID, sb.TaskID, string(sb.Status), sb.CreatedAt, sb.UpdatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveSnapshots persists a batch of snapshots.
func (r postgresExtendedRepository) SaveSnapshots(ctx context.Context, tx *sql.Tx, snapshots []*domain.Snapshot) error {
	for _, snap := range snapshots {
		if snap == nil {
			continue
		}
		payload, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("encode snapshot: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO snapshots (id, project_id, branch, stable, created_at, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			snap.ID, snap.ProjectID, snap.Branch, snap.Stable, snap.CreatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveAuditLogs persists a batch of audit logs.
func (r postgresExtendedRepository) SaveAuditLogs(ctx context.Context, tx *sql.Tx, logs []*domain.AuditLog) error {
	for _, log := range logs {
		if log == nil {
			continue
		}
		payload, err := json.Marshal(log)
		if err != nil {
			return fmt.Errorf("encode audit log: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_logs (id, project_id, action, resource_type, timestamp, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			log.ID, log.ProjectID, log.Action, log.ResourceType, log.Timestamp, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveAlerts persists a batch of alerts.
func (r postgresExtendedRepository) SaveAlerts(ctx context.Context, tx *sql.Tx, alerts []*domain.Alert) error {
	for _, alert := range alerts {
		if alert == nil {
			continue
		}
		payload, err := json.Marshal(alert)
		if err != nil {
			return fmt.Errorf("encode alert: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alerts (id, project_id, severity, type, timestamp, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			alert.ID, alert.ProjectID, alert.Severity, alert.Type, alert.Timestamp, payload); err != nil {
			return err
		}
	}
	return nil
}

// LoadContextInjections reads all context injections for a project.
func (r postgresExtendedRepository) LoadContextInjections(ctx context.Context, projectID string) ([]*domain.ContextInjection, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM context_injections WHERE project_id = $1 ORDER BY task_id, version, created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.ContextInjection
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var inj domain.ContextInjection
		if err := json.Unmarshal(payload, &inj); err != nil {
			return nil, fmt.Errorf("decode context injection: %w", err)
		}
		results = append(results, &inj)
	}
	return results, rows.Err()
}

// LoadSandboxes reads all sandboxes for a project.
func (r postgresExtendedRepository) LoadSandboxes(ctx context.Context, projectID string) ([]*domain.Sandbox, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM sandboxes WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.Sandbox
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var sb domain.Sandbox
		if err := json.Unmarshal(payload, &sb); err != nil {
			return nil, fmt.Errorf("decode sandbox: %w", err)
		}
		results = append(results, &sb)
	}
	return results, rows.Err()
}

// LoadSnapshots reads all snapshots for a project.
func (r postgresExtendedRepository) LoadSnapshots(ctx context.Context, projectID string) ([]*domain.Snapshot, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM snapshots WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.Snapshot
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var snap domain.Snapshot
		if err := json.Unmarshal(payload, &snap); err != nil {
			return nil, fmt.Errorf("decode snapshot: %w", err)
		}
		results = append(results, &snap)
	}
	return results, rows.Err()
}

// LoadAuditLogs reads all audit logs for a project.
func (r postgresExtendedRepository) LoadAuditLogs(ctx context.Context, projectID string) ([]*domain.AuditLog, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM audit_logs WHERE project_id = $1 ORDER BY timestamp`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.AuditLog
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var log domain.AuditLog
		if err := json.Unmarshal(payload, &log); err != nil {
			return nil, fmt.Errorf("decode audit log: %w", err)
		}
		results = append(results, &log)
	}
	return results, rows.Err()
}

// LoadAlerts reads all alerts for a project.
func (r postgresExtendedRepository) LoadAlerts(ctx context.Context, projectID string) ([]*domain.Alert, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM alerts WHERE project_id = $1 ORDER BY timestamp`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.Alert
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var alert domain.Alert
		if err := json.Unmarshal(payload, &alert); err != nil {
			return nil, fmt.Errorf("decode alert: %w", err)
		}
		results = append(results, &alert)
	}
	return results, rows.Err()
}

// ClearAll removes all data from the extended projection tables (used during full-state save).
func (r postgresExtendedRepository) ClearAll(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"alerts", "audit_logs", "snapshots", "sandboxes", "context_injections"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	return nil
}

// SaveHumanOverrides persists a batch of human overrides.
func (r postgresExtendedRepository) SaveHumanOverrides(ctx context.Context, tx *sql.Tx, overrides []*domain.HumanOverride) error {
	for _, o := range overrides {
		if o == nil {
			continue
		}
		payload, err := json.Marshal(o)
		if err != nil {
			return fmt.Errorf("encode human override: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO human_overrides (id, project_id, task_id, created_at, payload)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			o.ID, o.ProjectID, o.TaskID, o.CreatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveCodeLocks persists a batch of code locks.
func (r postgresExtendedRepository) SaveCodeLocks(ctx context.Context, tx *sql.Tx, locks []*domain.CodeLock) error {
	for _, l := range locks {
		if l == nil {
			continue
		}
		payload, err := json.Marshal(l)
		if err != nil {
			return fmt.Errorf("encode code lock: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_locks (id, project_id, path, created_at, payload)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			l.ID, l.ProjectID, l.Path, l.CreatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveConflictQueueEntries persists a batch of conflict queue entries.
func (r postgresExtendedRepository) SaveConflictQueueEntries(ctx context.Context, tx *sql.Tx, entries []*domain.ConflictQueueEntry) error {
	for _, e := range entries {
		if e == nil {
			continue
		}
		payload, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode conflict queue entry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO conflict_queue_entries (id, project_id, kind, status, created_at, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, payload = EXCLUDED.payload`,
			e.ID, e.ProjectID, e.Kind, e.Status, e.CreatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SavePreviews persists a batch of previews.
func (r postgresExtendedRepository) SavePreviews(ctx context.Context, tx *sql.Tx, previews []*domain.Preview) error {
	for _, p := range previews {
		if p == nil {
			continue
		}
		payload, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("encode preview: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO previews (id, project_id, status, created_at, updated_at, payload)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, payload = EXCLUDED.payload`,
			p.ID, p.ProjectID, p.Status, p.CreatedAt, p.UpdatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

// SaveCommunicationLogs persists a batch of communication logs.
func (r postgresExtendedRepository) SaveCommunicationLogs(ctx context.Context, tx *sql.Tx, logs []*domain.CommunicationLog) error {
	for _, l := range logs {
		if l == nil {
			continue
		}
		payload, err := json.Marshal(l)
		if err != nil {
			return fmt.Errorf("encode communication log: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO communication_logs (id, project_id, type, timestamp, payload)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`,
			l.ID, l.ProjectID, l.Type, l.Timestamp, payload); err != nil {
			return err
		}
	}
	return nil
}

// LoadHumanOverrides reads all human overrides for a project.
func (r postgresExtendedRepository) LoadHumanOverrides(ctx context.Context, projectID string) ([]*domain.HumanOverride, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM human_overrides WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.HumanOverride
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var o domain.HumanOverride
		if err := json.Unmarshal(payload, &o); err != nil {
			return nil, fmt.Errorf("decode human override: %w", err)
		}
		results = append(results, &o)
	}
	return results, rows.Err()
}

// LoadCodeLocks reads all code locks for a project.
func (r postgresExtendedRepository) LoadCodeLocks(ctx context.Context, projectID string) ([]*domain.CodeLock, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM code_locks WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.CodeLock
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var l domain.CodeLock
		if err := json.Unmarshal(payload, &l); err != nil {
			return nil, fmt.Errorf("decode code lock: %w", err)
		}
		results = append(results, &l)
	}
	return results, rows.Err()
}

// LoadConflictQueueEntries reads all conflict queue entries for a project.
func (r postgresExtendedRepository) LoadConflictQueueEntries(ctx context.Context, projectID string) ([]*domain.ConflictQueueEntry, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM conflict_queue_entries WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.ConflictQueueEntry
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var e domain.ConflictQueueEntry
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("decode conflict queue entry: %w", err)
		}
		results = append(results, &e)
	}
	return results, rows.Err()
}

// LoadPreviews reads all previews for a project.
func (r postgresExtendedRepository) LoadPreviews(ctx context.Context, projectID string) ([]*domain.Preview, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM previews WHERE project_id = $1 ORDER BY created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.Preview
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var p domain.Preview
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("decode preview: %w", err)
		}
		results = append(results, &p)
	}
	return results, rows.Err()
}

// LoadCommunicationLogs reads all communication logs for a project.
func (r postgresExtendedRepository) LoadCommunicationLogs(ctx context.Context, projectID string) ([]*domain.CommunicationLog, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	rows, err := r.raw.DB().QueryContext(ctx,
		`SELECT payload FROM communication_logs WHERE project_id = $1 ORDER BY timestamp`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*domain.CommunicationLog
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var l domain.CommunicationLog
		if err := json.Unmarshal(payload, &l); err != nil {
			return nil, fmt.Errorf("decode communication log: %w", err)
		}
		results = append(results, &l)
	}
	return results, rows.Err()
}
