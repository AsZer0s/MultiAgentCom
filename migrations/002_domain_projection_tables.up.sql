-- Migration 002: Core domain projection tables
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
);
