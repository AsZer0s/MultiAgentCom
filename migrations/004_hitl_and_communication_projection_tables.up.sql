-- Migration 004: HITL and communication projection tables
CREATE TABLE IF NOT EXISTS human_overrides (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    task_id text NOT NULL,
    created_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS human_overrides_project_idx ON human_overrides(project_id);

CREATE TABLE IF NOT EXISTS code_locks (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    path text NOT NULL,
    created_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS code_locks_project_idx ON code_locks(project_id);

CREATE TABLE IF NOT EXISTS conflict_queue_entries (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    created_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS conflict_queue_entries_project_idx ON conflict_queue_entries(project_id);

CREATE TABLE IF NOT EXISTS previews (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    status text NOT NULL,
    created_at timestamptz,
    updated_at timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS previews_project_idx ON previews(project_id);

CREATE TABLE IF NOT EXISTS communication_logs (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    type text NOT NULL,
    timestamp timestamptz,
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS communication_logs_project_idx ON communication_logs(project_id);
