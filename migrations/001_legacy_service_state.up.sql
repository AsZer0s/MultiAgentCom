-- Migration 001: Legacy service state table
CREATE TABLE IF NOT EXISTS service_state (
  key text PRIMARY KEY,
  payload jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
