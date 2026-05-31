-- db/outbox/schema.sql — transactional outbox table (v2).
--
-- Idempotent: safe to run on a fresh database AND as an in-place
-- upgrade from v1 (next_retry_at column + reshaped partial index).
--
-- v2 additions vs v1:
--   * next_retry_at column — per-row "ready to dispatch at" timestamp
--     so failed events can back off exponentially without spamming
--     the bus on every poll tick.
--   * Partial index rekeyed on next_retry_at so the polling SELECT
--     stays index-only as failed rows accumulate.

CREATE TABLE IF NOT EXISTS outbox (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  text        NOT NULL,
    aggregate_id    text        NOT NULL,
    event_type      text        NOT NULL,
    payload         bytea       NOT NULL,
    headers         jsonb,
    created_at      timestamptz NOT NULL DEFAULT NOW(),
    published_at    timestamptz,
    attempts        integer     NOT NULL DEFAULT 0,
    last_error      text,
    next_retry_at   timestamptz NOT NULL DEFAULT NOW()
);

-- v1 → v2 column upgrade: NULL on existing rows = "eligible now".
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS next_retry_at timestamptz NOT NULL DEFAULT NOW();

-- Replace the v1 created_at index with the v2 next_retry_at index.
-- DROP IF EXISTS no-ops cleanly on fresh installs.
DROP INDEX IF EXISTS outbox_unpublished_created_at_idx;

-- Partial index over the still-to-do set ordered by next_retry_at
-- — the polling SELECT touches only rows whose retry window has
-- arrived. created_at is the secondary key for fairness on ties.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON outbox (next_retry_at, created_at)
    WHERE published_at IS NULL;

-- Aggregate lookup index for app-side debugging / replay tooling.
CREATE INDEX IF NOT EXISTS outbox_aggregate_idx
    ON outbox (aggregate_type, aggregate_id);
