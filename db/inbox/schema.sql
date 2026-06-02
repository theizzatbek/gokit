-- db/inbox/schema.sql — transactional inbox table for effectively-
-- once consumers.
--
-- Idempotent: safe to run on a fresh database AND on every startup
-- (CREATE TABLE/INDEX IF NOT EXISTS).
--
-- The composite primary key (consumer, event_id) gives each consumer
-- its own dedup namespace at zero schema cost — multi-consumer
-- fan-out is the normal case for pub/sub.

CREATE TABLE IF NOT EXISTS inbox (
    consumer     text         NOT NULL,
    event_id     text         NOT NULL,
    processed_at timestamptz  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer, event_id)
);

-- Retention worker scans processed_at; PK index covers Process() lookups.
CREATE INDEX IF NOT EXISTS inbox_processed_at_idx ON inbox (processed_at);
