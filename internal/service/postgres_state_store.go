package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"multiagentcom/internal/domain"
	"multiagentcom/internal/store"
)

type postgresDomainStateStore struct {
	raw *store.PostgresStore
}

func newPostgresDomainStateStore(dsn string) stateStore {
	return postgresDomainStateStore{raw: store.NewPostgresStore(dsn)}
}

func (s postgresDomainStateStore) LoadState(ctx context.Context) (*persistedServiceState, error) {
	if s.raw == nil {
		return nil, nil
	}
	if err := s.raw.Migrate(ctx); err != nil {
		return nil, err
	}
	legacy, err := s.loadLegacyState(ctx)
	if err != nil {
		return nil, err
	}
	hasProjection, err := s.hasProjectionData(ctx)
	if err != nil {
		return nil, err
	}
	if !hasProjection {
		if legacy != nil {
			if err := s.saveProjection(ctx, legacy); err != nil {
				return nil, err
			}
		}
		return legacy, nil
	}
	state := legacy
	if state == nil {
		state = &persistedServiceState{Version: 1}
	}
	if err := s.loadProjection(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s postgresDomainStateStore) SaveState(ctx context.Context, state *persistedServiceState) error {
	if s.raw == nil || state == nil {
		return nil
	}
	if err := s.raw.Migrate(ctx); err != nil {
		return err
	}
	return s.saveProjectionAndLegacy(ctx, state)
}

func (s postgresDomainStateStore) loadLegacyState(ctx context.Context) (*persistedServiceState, error) {
	payload, err := s.raw.Load(ctx)
	if err != nil || len(payload) == 0 {
		return nil, err
	}
	var state persistedServiceState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("decode legacy service state: %w", err)
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported service state version %d", state.Version)
	}
	return &state, nil
}

func (s postgresDomainStateStore) hasProjectionData(ctx context.Context) (bool, error) {
	var count int
	for _, table := range []string{"projects", "requirements", "plans", "contracts", "tasks", "agent_runs", "artifacts"} {
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := s.raw.DB().QueryRowContext(ctx, query).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s postgresDomainStateStore) saveProjection(ctx context.Context, state *persistedServiceState) error {
	tx, err := s.raw.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveServiceStateProjection(ctx, tx, state); err != nil {
		return err
	}
	return tx.Commit()
}

func (s postgresDomainStateStore) saveProjectionAndLegacy(ctx context.Context, state *persistedServiceState) error {
	tx, err := s.raw.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveServiceStateProjection(ctx, tx, state); err != nil {
		return err
	}
	if err := saveLegacyServiceState(ctx, tx, state); err != nil {
		return err
	}
	return tx.Commit()
}

func saveLegacyServiceState(ctx context.Context, tx *sql.Tx, state *persistedServiceState) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode service state: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
	INSERT INTO service_state (key, payload, updated_at)
	VALUES ($1, $2::jsonb, now())
	ON CONFLICT (key) DO UPDATE SET payload = EXCLUDED.payload, updated_at = now()
	`, store.PostgresServiceStateKey(), payload)
	return err
}

func (s postgresDomainStateStore) loadProjection(ctx context.Context, state *persistedServiceState) error {
	return loadServiceStateProjection(ctx, s.raw.DB(), state)
}

func saveServiceStateProjection(ctx context.Context, tx *sql.Tx, state *persistedServiceState) error {
	for _, table := range []string{"artifact_order", "artifacts", "run_order", "agent_runs", "task_order", "tasks", "contracts", "plans", "requirements", "projects"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	if err := saveProjects(ctx, tx, state.Projects); err != nil {
		return err
	}
	if err := saveRequirements(ctx, tx, state.Requirements); err != nil {
		return err
	}
	if err := savePlans(ctx, tx, state.Plans); err != nil {
		return err
	}
	if err := saveContracts(ctx, tx, state.Contracts); err != nil {
		return err
	}
	if err := saveTasks(ctx, tx, state.Tasks, state.TaskOrder); err != nil {
		return err
	}
	if err := saveRuns(ctx, tx, state.Runs, state.RunOrder); err != nil {
		return err
	}
	if err := saveArtifacts(ctx, tx, state.Artifacts, state.ArtifactOrder); err != nil {
		return err
	}
	return nil
}

func loadServiceStateProjection(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	if err := loadProjects(ctx, db, state); err != nil {
		return err
	}
	if err := loadRequirements(ctx, db, state); err != nil {
		return err
	}
	if err := loadPlans(ctx, db, state); err != nil {
		return err
	}
	if err := loadContracts(ctx, db, state); err != nil {
		return err
	}
	if err := loadTasks(ctx, db, state); err != nil {
		return err
	}
	if err := loadRuns(ctx, db, state); err != nil {
		return err
	}
	if err := loadArtifacts(ctx, db, state); err != nil {
		return err
	}
	return nil
}

func saveProjects(ctx context.Context, tx *sql.Tx, projects map[string]*domain.Project) error {
	for _, project := range projects {
		payload, err := json.Marshal(project)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, created_at, updated_at, payload) VALUES ($1, $2, $3, $4::jsonb)`, project.ID, project.CreatedAt, project.UpdatedAt, payload); err != nil {
			return err
		}
	}
	return nil
}

func saveRequirements(ctx context.Context, tx *sql.Tx, requirements map[string][]*domain.Requirement) error {
	for projectID, items := range requirements {
		for position, requirement := range items {
			payload, err := json.Marshal(requirement)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO requirements (id, project_id, position, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, requirement.ID, projectID, position, requirement.CreatedAt, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

func savePlans(ctx context.Context, tx *sql.Tx, plans map[string][]*domain.Plan) error {
	for projectID, items := range plans {
		for _, plan := range items {
			payload, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO plans (id, project_id, version, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, plan.ID, projectID, plan.Version, plan.CreatedAt, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveContracts(ctx context.Context, tx *sql.Tx, contracts map[string][]*domain.Contract) error {
	for projectID, items := range contracts {
		for _, contract := range items {
			payload, err := json.Marshal(contract)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO contracts (id, project_id, version, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, contract.ID, projectID, contract.Version, contract.CreatedAt, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveTasks(ctx context.Context, tx *sql.Tx, tasks map[string]*domain.Task, taskOrder map[string][]string) error {
	for _, task := range tasks {
		payload, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, project_id, status, assignee_agent, created_at, updated_at, payload) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, task.ID, task.ProjectID, string(task.Status), task.AssigneeAgent, task.CreatedAt, task.UpdatedAt, payload); err != nil {
			return err
		}
	}
	for projectID, ids := range taskOrder {
		for position, taskID := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_order (project_id, position, task_id) VALUES ($1, $2, $3)`, projectID, position, taskID); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveRuns(ctx context.Context, tx *sql.Tx, runs map[string]*domain.AgentRun, runOrder map[string][]string) error {
	for _, run := range runs {
		payload, err := json.Marshal(run)
		if err != nil {
			return err
		}
		updatedAt := run.EndedAt
		if updatedAt.IsZero() {
			updatedAt = run.StartedAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs (id, project_id, task_id, status, created_at, updated_at, payload) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, run.ID, run.ProjectID, run.TaskID, string(run.Status), run.StartedAt, updatedAt, payload); err != nil {
			return err
		}
	}
	for projectID, ids := range runOrder {
		for position, runID := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO run_order (project_id, position, run_id) VALUES ($1, $2, $3)`, projectID, position, runID); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveArtifacts(ctx context.Context, tx *sql.Tx, artifacts map[string]*domain.Artifact, artifactOrder map[string][]string) error {
	for _, artifact := range artifacts {
		payload, err := json.Marshal(artifact)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts (id, project_id, run_id, created_at, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`, artifact.ID, artifact.ProjectID, artifact.RunID, artifact.CreatedAt, payload); err != nil {
			return err
		}
	}
	for projectID, ids := range artifactOrder {
		for position, artifactID := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_order (project_id, position, artifact_id) VALUES ($1, $2, $3)`, projectID, position, artifactID); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadProjects(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM projects ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	projects := make(map[string]*domain.Project)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var project domain.Project
		if err := json.Unmarshal(payload, &project); err != nil {
			return err
		}
		projects[project.ID] = &project
	}
	if err := rows.Err(); err != nil {
		return err
	}
	state.Projects = projects
	return nil
}

func loadRequirements(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM requirements ORDER BY project_id, position`)
	if err != nil {
		return err
	}
	defer rows.Close()
	requirements := make(map[string][]*domain.Requirement)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var requirement domain.Requirement
		if err := json.Unmarshal(payload, &requirement); err != nil {
			return err
		}
		requirements[requirement.ProjectID] = append(requirements[requirement.ProjectID], &requirement)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	state.Requirements = requirements
	return nil
}

func loadPlans(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM plans ORDER BY project_id, version, created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	plans := make(map[string][]*domain.Plan)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var plan domain.Plan
		if err := json.Unmarshal(payload, &plan); err != nil {
			return err
		}
		plans[plan.ProjectID] = append(plans[plan.ProjectID], &plan)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	state.Plans = plans
	return nil
}

func loadContracts(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM contracts ORDER BY project_id, version, created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	contracts := make(map[string][]*domain.Contract)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var contract domain.Contract
		if err := json.Unmarshal(payload, &contract); err != nil {
			return err
		}
		contracts[contract.ProjectID] = append(contracts[contract.ProjectID], &contract)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	state.Contracts = contracts
	return nil
}

func loadTasks(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM tasks`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tasks := make(map[string]*domain.Task)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var task domain.Task
		if err := json.Unmarshal(payload, &task); err != nil {
			return err
		}
		tasks[task.ID] = &task
	}
	if err := rows.Err(); err != nil {
		return err
	}
	order, err := loadOrder(ctx, db, "task_order", "task_id")
	if err != nil {
		return err
	}
	state.Tasks = tasks
	state.TaskOrder = order
	return nil
}

func loadRuns(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM agent_runs`)
	if err != nil {
		return err
	}
	defer rows.Close()
	runs := make(map[string]*domain.AgentRun)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var run domain.AgentRun
		if err := json.Unmarshal(payload, &run); err != nil {
			return err
		}
		runs[run.ID] = &run
	}
	if err := rows.Err(); err != nil {
		return err
	}
	order, err := loadOrder(ctx, db, "run_order", "run_id")
	if err != nil {
		return err
	}
	state.Runs = runs
	state.RunOrder = order
	return nil
}

func loadArtifacts(ctx context.Context, db *sql.DB, state *persistedServiceState) error {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM artifacts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	artifacts := make(map[string]*domain.Artifact)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var artifact domain.Artifact
		if err := json.Unmarshal(payload, &artifact); err != nil {
			return err
		}
		artifacts[artifact.ID] = &artifact
	}
	if err := rows.Err(); err != nil {
		return err
	}
	order, err := loadOrder(ctx, db, "artifact_order", "artifact_id")
	if err != nil {
		return err
	}
	state.Artifacts = artifacts
	state.ArtifactOrder = order
	return nil
}

func loadOrder(ctx context.Context, db *sql.DB, table, idColumn string) (map[string][]string, error) {
	query := fmt.Sprintf("SELECT project_id, %s FROM %s ORDER BY project_id, position", idColumn, table)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := make(map[string][]string)
	for rows.Next() {
		var projectID string
		var id string
		if err := rows.Scan(&projectID, &id); err != nil {
			return nil, err
		}
		order[projectID] = append(order[projectID], id)
	}
	return order, rows.Err()
}
