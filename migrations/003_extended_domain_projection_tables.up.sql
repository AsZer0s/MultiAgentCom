-- Migration 003: Extended domain projection tables
CREATE TABLE IF NOT EXISTS context_injections (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    task_id text NOT NULL,
    version int NOT NULL,
    created_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS context_injections_project_task_idx ON context_injections(project_id, task_id);

CREATE TABLE IF NOT EXISTS sandboxes (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    run_id text NOT NULL,
    task_id text NOT NULL,
    status text NOT NULL,
    created_at timestamptz,
    updated_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS sandboxes_project_idx ON sandboxes(project_id);
CREATE INDEX IF NOT EXISTS sandboxes_run_idx ON sandboxes(run_id);

CREATE TABLE IF NOT EXISTS snapshots (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    branch text NOT NULL,
    stable boolean NOT NULL DEFAULT false,
    created_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS snapshots_project_idx ON snapshots(project_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    timestamp timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_logs_project_idx ON audit_logs(project_id);

CREATE TABLE IF NOT EXISTS alerts (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    severity text NOT NULL,
    type text NOT NULL,
    timestamp timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS alerts_project_idx ON alerts(project_id);
