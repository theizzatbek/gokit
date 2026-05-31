-- db/outbox/schema.sql — transactional outbox table.
--
-- The Worker polls for unpublished rows via
-- `SELECT ... WHERE published_at IS NULL FOR UPDATE SKIP LOCKED`
-- so multiple replicas can drain the same outbox without stepping
-- on each other's rows. Indexes are tuned for that polling path.

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
    last_error      text
);

-- Partial index over the "still to do" set. `created_at` is the
-- secondary ordering key the Worker uses for fairness — first in,
-- first out within a single fetch batch.
CREATE INDEX IF NOT EXISTS outbox_unpublished_created_at_idx
    ON outbox (created_at)
    WHERE published_at IS NULL;

-- Aggregate lookup index for app-side debugging / replay tooling.
-- Not on the hot path; cheap to maintain.
CREATE INDEX IF NOT EXISTS outbox_aggregate_idx
    ON outbox (aggregate_type, aggregate_id);
