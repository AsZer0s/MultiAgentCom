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

var (
	errRepositoryContractNotFound = errors.New("repository contract not found")
	errRepositoryTaskNotFound     = errors.New("repository task not found")
)

type postgresPlanContractTaskRepository struct {
	raw *store.PostgresStore
}

func (r postgresPlanContractTaskRepository) beginTx(ctx context.Context) (*sql.Tx, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	return r.raw.DB().BeginTx(ctx, nil)
}

func (r postgresPlanContractTaskRepository) GeneratePlan(ctx context.Context, project *domain.Project, plan *domain.Plan, task *domain.Task, taskPosition int, legacy *persistedServiceState) error {
	if project == nil {
		return fmt.Errorf("project is required")
	}
	if plan == nil {
		return fmt.Errorf("plan is required")
	}
	if task == nil {
		return fmt.Errorf("task is required")
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
	if err := lockRepositoryProject(ctx, tx, project.ID); err != nil {
		return err
	}
	if err := insertRepositoryPlan(ctx, tx, plan); err != nil {
		return err
	}
	if err := insertRepositoryTask(ctx, tx, task); err != nil {
		return err
	}
	if err := insertRepositoryTaskOrder(ctx, tx, project.ID, task.ID, taskPosition); err != nil {
		return err
	}
	if err := updateRepositoryProject(ctx, tx, project); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresPlanContractTaskRepository) GenerateContract(ctx context.Context, project *domain.Project, contract *domain.Contract, legacy *persistedServiceState) error {
	if project == nil {
		return fmt.Errorf("project is required")
	}
	if contract == nil {
		return fmt.Errorf("contract is required")
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
	if err := lockRepositoryProject(ctx, tx, project.ID); err != nil {
		return err
	}
	if err := insertRepositoryContract(ctx, tx, contract); err != nil {
		return err
	}
	if err := updateRepositoryProject(ctx, tx, project); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresPlanContractTaskRepository) SaveTasks(ctx context.Context, project *domain.Project, tasks []*domain.Task, taskPositions map[string]int, legacy *persistedServiceState) error {
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
	if err := lockRepositoryProject(ctx, tx, project.ID); err != nil {
		return err
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := insertRepositoryTask(ctx, tx, task); err != nil {
			return err
		}
		position, ok := taskPositions[task.ID]
		if !ok {
			return fmt.Errorf("task position is required for %s", task.ID)
		}
		if err := insertRepositoryTaskOrder(ctx, tx, project.ID, task.ID, position); err != nil {
			return err
		}
	}
	if err := updateRepositoryProject(ctx, tx, project); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, legacy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r postgresPlanContractTaskRepository) ListContracts(ctx context.Context, projectID string) ([]*domain.Contract, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	if err := ensureRepositoryProjectExists(ctx, r.raw.DB(), projectID); err != nil {
		return nil, err
	}
	rows, err := r.raw.DB().QueryContext(ctx, `SELECT payload FROM contracts WHERE project_id = $1 ORDER BY version, created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contracts := make([]*domain.Contract, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var contract domain.Contract
		if err := json.Unmarshal(payload, &contract); err != nil {
			return nil, fmt.Errorf("decode contract: %w", err)
		}
		contracts = append(contracts, &contract)
	}
	return contracts, rows.Err()
}

func (r postgresPlanContractTaskRepository) GetContract(ctx context.Context, projectID, contractID string) (*domain.Contract, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	var payload []byte
	if err := r.raw.DB().QueryRowContext(ctx, `SELECT payload FROM contracts WHERE project_id = $1 AND id = $2`, projectID, contractID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errRepositoryContractNotFound
		}
		return nil, err
	}
	var contract domain.Contract
	if err := json.Unmarshal(payload, &contract); err != nil {
		return nil, fmt.Errorf("decode contract: %w", err)
	}
	return &contract, nil
}

func (r postgresPlanContractTaskRepository) ListTasks(ctx context.Context, projectID string) ([]*domain.Task, error) {
	if r.raw == nil || r.raw.DB() == nil {
		return nil, fmt.Errorf("postgres store is not configured")
	}
	if err := r.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	if err := ensureRepositoryProjectExists(ctx, r.raw.DB(), projectID); err != nil {
		return nil, err
	}
	rows, err := r.raw.DB().QueryContext(ctx, `
		SELECT t.payload
		FROM task_order o
		JOIN tasks t ON t.id = o.task_id
		WHERE o.project_id = $1
		ORDER BY o.position, t.created_at, t.id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]*domain.Task, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var task domain.Task
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, fmt.Errorf("decode task: %w", err)
		}
		tasks = append(tasks, &task)
	}
	return tasks, rows.Err()
}

func lockRepositoryProject(ctx context.Context, tx *sql.Tx, projectID string) error {
	var exists string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errRepositoryProjectNotFound
		}
		return err
	}
	return nil
}

func ensureRepositoryProjectExists(ctx context.Context, db *sql.DB, projectID string) error {
	var exists string
	if err := db.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = $1`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errRepositoryProjectNotFound
		}
		return err
	}
	return nil
}

func updateRepositoryProject(ctx context.Context, tx *sql.Tx, project *domain.Project) error {
	payload, err := json.Marshal(project)
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at = $2, payload = $3::jsonb WHERE id = $1`, project.ID, project.UpdatedAt, payload)
	return err
}

func insertRepositoryPlan(ctx context.Context, tx *sql.Tx, plan *domain.Plan) error {
	payload, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plans (id, project_id, version, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, plan.ID, plan.ProjectID, plan.Version, plan.CreatedAt, payload)
	return err
}

func insertRepositoryContract(ctx context.Context, tx *sql.Tx, contract *domain.Contract) error {
	payload, err := json.Marshal(contract)
	if err != nil {
		return fmt.Errorf("encode contract: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO contracts (id, project_id, version, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, contract.ID, contract.ProjectID, contract.Version, contract.CreatedAt, payload)
	return err
}

func insertRepositoryTask(ctx context.Context, tx *sql.Tx, task *domain.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("encode task: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks (id, project_id, status, assignee_agent, created_at, updated_at, payload) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, task.ID, task.ProjectID, string(task.Status), task.AssigneeAgent, task.CreatedAt, task.UpdatedAt, payload)
	return err
}

func insertRepositoryTaskOrder(ctx context.Context, tx *sql.Tx, projectID, taskID string, position int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_order (project_id, position, task_id) VALUES ($1, $2, $3)`, projectID, position, taskID)
	return err
}
