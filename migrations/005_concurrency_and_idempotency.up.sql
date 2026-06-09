-- Migration 005: Concurrency and idempotency tables
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key_text text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
