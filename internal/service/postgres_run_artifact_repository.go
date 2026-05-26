package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"multiagentcom/internal/domain"
	"multiagentcom/internal/store"
)

var (
	errRepositoryRunNotFound      = errors.New("repository run not found")
	errRepositoryArtifactNotFound = errors.New("repository artifact not found")
)

type postgresRunArtifactRepository struct {
	raw *store.PostgresStore
}

func (r postgresRunArtifactRepository) beginTx(ctx context.Context) (*sql.Tx, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	return r.raw.DB().BeginTx(ctx, nil)
}

func (r postgresRunArtifactRepository) StartRuns(ctx context.Context, items []runStartPersistence, legacy *persistedServiceState) error {
	if legacy == nil {
		return fmt.Errorf("legacy state is required")
	}
	tx, err := r.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	lockedProjects := map[string]struct{}{}
	for _, item := range items {
		if item.Task == nil {
			return fmt.Errorf("task is required")
		}
		if item.Run == nil {
			return fmt.Errorf("run is required")
		}
		if item.Task.ProjectID != item.Run.ProjectID {
			return fmt.Errorf("task/run project mismatch")
		}
		if _, ok := lockedProjects[item.Run.ProjectID]; !ok {
			if err := lockRepositoryProject(ctx, tx, item.Run.ProjectID); err != nil {
				return err
			}
			lockedProjects[item.Run.ProjectID] = struct{}{}
		}
		if err := updateRepositoryTask(ctx, tx, item.Task); err != nil {
			return err
		}
		if err := insertRepositoryRun(ctx, tx, item.Run); err != nil {
			return err
		}
		if err := insertRepositoryRunOrder(ctx, tx, item.Run.ProjectID, item.Run.ID, item.RunPosition); err != nil {
			return err
		}
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresRunArtifactRepository) CompleteRun(ctx context.Context, task *domain.Task, run *domain.AgentRun, artifact *domain.Artifact, artifactPosition int, legacy *persistedServiceState) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}
	if run == nil {
		return fmt.Errorf("run is required")
	}
	if artifact == nil {
		return fmt.Errorf("artifact is required")
	}
	if legacy == nil {
		return fmt.Errorf("legacy state is required")
	}
	tx, err := r.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := lockRepositoryProject(ctx, tx, run.ProjectID); err != nil {
		return err
	}
	if err := updateRepositoryTask(ctx, tx, task); err != nil {
		return err
	}
	if err := updateRepositoryRun(ctx, tx, run); err != nil {
		return err
	}
	if err := insertRepositoryArtifact(ctx, tx, artifact); err != nil {
		return err
	}
	if err := insertRepositoryArtifactOrder(ctx, tx, artifact.ProjectID, artifact.ID, artifactPosition); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresRunArtifactRepository) FailRun(ctx context.Context, task *domain.Task, run *domain.AgentRun, legacy *persistedServiceState) error {
	if run == nil {
		return fmt.Errorf("run is required")
	}
	if legacy == nil {
		return fmt.Errorf("legacy state is required")
	}
	tx, err := r.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := lockRepositoryProject(ctx, tx, run.ProjectID); err != nil {
		return err
	}
	if task != nil {
		if err := updateRepositoryTask(ctx, tx, task); err != nil {
			return err
		}
	}
	if err := updateRepositoryRun(ctx, tx, run); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresRunArtifactRepository) GetRun(ctx context.Context, projectID, runID string) (*domain.AgentRun, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	if err := r.raw.DB().QueryRowContext(ctx, `SELECT payload FROM agent_runs WHERE project_id = $1 AND id = $2`, projectID, runID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errRepositoryRunNotFound
		}
		return nil, err
	}
	var run domain.AgentRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, fmt.Errorf("decode run: %w", err)
	}
	return &run, nil
}

func (r postgresRunArtifactRepository) GetArtifactsForRun(ctx context.Context, projectID string, artifactIDs []string) ([]*domain.Artifact, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	artifacts := make([]*domain.Artifact, 0, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		artifactID = strings.TrimSpace(artifactID)
		if artifactID == "" {
			continue
		}
		artifact, err := r.GetArtifact(ctx, projectID, artifactID)
		if err != nil {
			if errors.Is(err, errRepositoryArtifactNotFound) {
				continue
			}
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (r postgresRunArtifactRepository) GetArtifact(ctx context.Context, projectID, artifactID string) (*domain.Artifact, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	if err := r.raw.DB().QueryRowContext(ctx, `SELECT payload FROM artifacts WHERE project_id = $1 AND id = $2`, projectID, artifactID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errRepositoryArtifactNotFound
		}
		return nil, err
	}
	var artifact domain.Artifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return nil, fmt.Errorf("decode artifact: %w", err)
	}
	return &artifact, nil
}

func (r postgresRunArtifactRepository) LatestExportableArtifact(ctx context.Context, projectID string) (*domain.AgentRun, *domain.Artifact, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, nil, err
	}
	if err := ensureRepositoryProjectExists(ctx, r.raw.DB(), projectID); err != nil {
		return nil, nil, err
	}
	rows, err := r.raw.DB().QueryContext(ctx, `
		SELECT r.payload
		FROM run_order o
		JOIN agent_runs r ON r.id = o.run_id
		WHERE o.project_id = $1 AND r.status = $2
		ORDER BY o.position DESC
	`, projectID, string(domain.RunStatusSucceeded))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, nil, err
		}
		var run domain.AgentRun
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, nil, fmt.Errorf("decode run: %w", err)
		}
		artifacts, err := r.GetArtifactsForRun(ctx, projectID, run.ArtifactIDs)
		if err != nil {
			return nil, nil, err
		}
		if len(artifacts) == 0 {
			continue
		}
		return &run, artifacts[len(artifacts)-1], nil
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return nil, nil, errRepositoryArtifactNotFound
}

func (r postgresRunArtifactRepository) SaveLegacy(ctx context.Context, legacy *persistedServiceState) error {
	if legacy == nil {
		return fmt.Errorf("legacy state is required")
	}
	tx, err := r.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func updateRepositoryTask(ctx context.Context, tx *sql.Tx, task *domain.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("encode task: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET status = $2, assignee_agent = $3, updated_at = $4, payload = $5::jsonb WHERE id = $1`, task.ID, string(task.Status), task.AssigneeAgent, task.UpdatedAt, payload)
	return err
}

func insertRepositoryRun(ctx context.Context, tx *sql.Tx, run *domain.AgentRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_runs (id, project_id, task_id, status, created_at, updated_at, payload) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, run.ID, run.ProjectID, run.TaskID, string(run.Status), run.StartedAt, repositoryRunUpdatedAt(run), payload)
	return err
}

func updateRepositoryRun(ctx context.Context, tx *sql.Tx, run *domain.AgentRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_runs SET status = $2, updated_at = $3, payload = $4::jsonb WHERE id = $1`, run.ID, string(run.Status), repositoryRunUpdatedAt(run), payload)
	return err
}

func insertRepositoryRunOrder(ctx context.Context, tx *sql.Tx, projectID, runID string, position int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO run_order (project_id, position, run_id) VALUES ($1, $2, $3)`, projectID, position, runID)
	return err
}

func insertRepositoryArtifact(ctx context.Context, tx *sql.Tx, artifact *domain.Artifact) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artifacts (id, project_id, run_id, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, artifact.ID, artifact.ProjectID, artifact.RunID, artifact.CreatedAt, payload)
	return err
}

func insertRepositoryArtifactOrder(ctx context.Context, tx *sql.Tx, projectID, artifactID string, position int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO artifact_order (project_id, position, artifact_id) VALUES ($1, $2, $3)`, projectID, position, artifactID)
	return err
}

func repositoryRunUpdatedAt(run *domain.AgentRun) interface{} {
	if run.EndedAt.IsZero() {
		return run.StartedAt
	}
	return run.EndedAt
}
