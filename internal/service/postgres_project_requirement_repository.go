package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"multiagentcom/internal/domain"
	"multiagentcom/internal/store"
)

var errRepositoryProjectNotFound = errors.New("repository project not found")

type postgresProjectRequirementRepository struct {
	raw *store.PostgresStore
}

func (r postgresProjectRequirementRepository) beginTx(ctx context.Context) (*sql.Tx, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	return r.raw.DB().BeginTx(ctx, nil)
}

func (r postgresProjectRequirementRepository) CreateProject(ctx context.Context, project *domain.Project, legacy *persistedServiceState) error {
	if project == nil {
		return fmt.Errorf("project is required")
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
	payload, err := json.Marshal(project)
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, created_at, updated_at, payload) VALUES ($1, $2, $3, $4::jsonb)`, project.ID, project.CreatedAt, project.UpdatedAt, payload); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresProjectRequirementRepository) AddRequirement(ctx context.Context, project *domain.Project, requirement *domain.Requirement, position int, legacy *persistedServiceState) error {
	if project == nil {
		return fmt.Errorf("project is required")
	}
	if requirement == nil {
		return fmt.Errorf("requirement is required")
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
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, project.ID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errRepositoryProjectNotFound
		}
		return err
	}
	requirementPayload, err := json.Marshal(requirement)
	if err != nil {
		return fmt.Errorf("encode requirement: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO requirements (id, project_id, position, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, requirement.ID, project.ID, position, requirement.CreatedAt, requirementPayload); err != nil {
		return err
	}
	projectPayload, err := json.Marshal(project)
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = $2, payload = $3::jsonb WHERE id = $1`, projectID, project.UpdatedAt, projectPayload); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresProjectRequirementRepository) ListProjects(ctx context.Context) ([]*domain.Project, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	rows, err := r.raw.DB().QueryContext(ctx, `SELECT payload FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]*domain.Project, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var project domain.Project
		if err := json.Unmarshal(payload, &project); err != nil {
			return nil, fmt.Errorf("decode project: %w", err)
		}
		projects = append(projects, &project)
	}
	return projects, rows.Err()
}

func (r postgresProjectRequirementRepository) GetProject(ctx context.Context, projectID string) (*domain.Project, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	if err := r.raw.DB().QueryRowContext(ctx, `SELECT payload FROM projects WHERE id = $1`, projectID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errRepositoryProjectNotFound
		}
		return nil, err
	}
	var project domain.Project
	if err := json.Unmarshal(payload, &project); err != nil {
		return nil, fmt.Errorf("decode project: %w", err)
	}
	return &project, nil
}

func (r postgresProjectRequirementRepository) ListRequirements(ctx context.Context, projectID string) ([]*domain.Requirement, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	var exists string
	if err := r.raw.DB().QueryRowContext(ctx, `SELECT id FROM projects WHERE id = $1`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errRepositoryProjectNotFound
		}
		return nil, err
	}
	rows, err := r.raw.DB().QueryContext(ctx, `SELECT payload FROM requirements WHERE project_id = $1 ORDER BY position, created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requirements := make([]*domain.Requirement, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var req domain.Requirement
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode requirement: %w", err)
		}
		requirements = append(requirements, &req)
	}
	return requirements, rows.Err()
}
