CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     TEXT NOT NULL,
    target_url   TEXT NOT NULL,
    secret_enc   BYTEA NOT NULL,
    event_types  TEXT[] NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','disabled')),
    description  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_subscriptions_owner
    ON webhook_subscriptions (owner_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_webhook_subscriptions_events
    ON webhook_subscriptions USING GIN (event_types)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id   UUID NOT NULL REFERENCES webhook_subscriptions(id)
                      ON DELETE CASCADE,
    event_id          UUID NOT NULL,
    event_type        TEXT NOT NULL,
    payload           BYTEA NOT NULL,
    headers           JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts          INT NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','delivered','dlq')),
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status_code  INT,
    last_error        TEXT NOT NULL DEFAULT '',
    delivered_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subscription_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due
    ON webhook_deliveries (next_attempt_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status
    ON webhook_deliveries (status, created_at);
