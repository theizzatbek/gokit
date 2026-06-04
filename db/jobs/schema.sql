-- db/jobs/schema.sql — delayed-job queue table.
--
-- Idempotent — safe to apply repeatedly.
--
-- State machine:
--   queued    → running   (worker claims via SKIP LOCKED)
--   queued    → cancelled (operator Cancel)
--   running   → done      (handler returned nil)
--   running   → queued    (handler returned err; attempts++, run_at += backoff)
--   running   → failed    (handler returned err AND attempts >= max_attempts)

CREATE TABLE IF NOT EXISTS jobs (
    id            bigserial   PRIMARY KEY,
    type          text        NOT NULL,
    queue         text        NOT NULL DEFAULT 'default',
    payload       jsonb       NOT NULL,
    run_at        timestamptz NOT NULL DEFAULT NOW(),
    state         text        NOT NULL DEFAULT 'queued',
    attempts      integer     NOT NULL DEFAULT 0,
    max_attempts  integer     NOT NULL DEFAULT 25,
    last_error    text,
    locked_by     text,
    locked_at     timestamptz,
    created_at    timestamptz NOT NULL DEFAULT NOW(),
    finished_at   timestamptz,
    -- v2 additions: optional priority + opaque caller-supplied
    -- dedup-key. Both default to "no preference" so upgrade is
    -- transparent — existing rows keep behaving as before.
    priority      integer     NOT NULL DEFAULT 0,
    dedup_key     text
);

-- v1 → v2 column upgrade for in-place migrations.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS dedup_key text;

-- Pending-rows index: covers the worker's hot path
-- (state='queued' AND run_at <= NOW() ORDER BY priority DESC, run_at).
-- Old idx_jobs_pending (queue, run_at) is dropped because the
-- new ORDER BY needs priority first; CREATE INDEX is safe to
-- re-issue idempotently.
DROP INDEX IF EXISTS idx_jobs_pending;
CREATE INDEX IF NOT EXISTS idx_jobs_pending
    ON jobs (queue, priority DESC, run_at)
    WHERE state = 'queued';

-- Operator-friendly indexes for triage queries.
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs (type);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs (state) WHERE state IN ('running','failed','cancelled');

-- Dedup uniqueness over (type, dedup_key) BUT only while the row
-- is queued. Cancelled / done / failed rows leave the partial
-- index so re-scheduling the same dedup_key after completion
-- always inserts a fresh row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedup_queued
    ON jobs (type, dedup_key)
    WHERE state = 'queued' AND dedup_key IS NOT NULL;
