-- db/jobs/schema.sql — delayed-job queue table.
--
-- Idempotent — safe to apply repeatedly.
--
-- State machine:
--   queued  → running (worker claims via SKIP LOCKED)
--   running → done   (handler returned nil)
--   running → queued (handler returned err; attempts++, run_at += backoff)
--   running → failed (handler returned err AND attempts >= max_attempts)

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
    finished_at   timestamptz
);

-- Pending-rows index: covers the worker's hot path
-- (state='queued' AND run_at <= NOW() ORDER BY run_at).
CREATE INDEX IF NOT EXISTS idx_jobs_pending
    ON jobs (queue, run_at)
    WHERE state = 'queued';

-- Operator-friendly indexes for triage queries.
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs (type);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs (state) WHERE state IN ('running','failed');
