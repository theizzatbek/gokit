-- audit/auditpg/schema.sql — audit-trail table.
-- Idempotent — safe to apply repeatedly.

CREATE TABLE IF NOT EXISTS audit_events (
    id            uuid        PRIMARY KEY,
    occurred_at   timestamptz NOT NULL,
    service_name  text,
    actor_subject text,
    actor_type    text,
    actor_ip      text,
    actor_ua      text,
    action        text        NOT NULL,
    target_type   text,
    target_id     text,
    target_name   text,
    outcome       text        NOT NULL,
    metadata      jsonb,
    prev_hash     bytea,
    hash          bytea
);

-- Most queries are time-range scans for an actor or action; covering
-- the hot paths keeps admin tooling snappy on multi-month tables.
CREATE INDEX IF NOT EXISTS idx_audit_occurred_at ON audit_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_events (actor_subject)
    WHERE actor_subject IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events (action);
CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_events (target_type, target_id);
