-- auth/apikeypg/schema.sql — Postgres-backed KeyStore.
--
-- The key_hash column stores the HMAC-SHA256(plain_key, kit_secret) so
-- a leaked DB doesn't reveal raw keys without the kit's secret. PRIMARY
-- KEY makes Lookup an O(1) tuple touch.
--
-- last_used_at is intentionally OPTIONAL — bumping it on every Lookup
-- would turn an idempotent read into a transactional write under load.
-- Implementations that need per-request audit can bolt on a separate
-- async writer.

CREATE TABLE IF NOT EXISTS auth_api_keys (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash      bytea       NOT NULL UNIQUE,
    key_prefix    text        NOT NULL DEFAULT '',
    subject       text        NOT NULL,
    scopes        text[]      NOT NULL DEFAULT '{}',
    role          text        NOT NULL DEFAULT '',
    description   text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT NOW(),
    expires_at    timestamptz,
    revoked_at    timestamptz,
    last_used_at  timestamptz
);

-- key_prefix stores a short, human-recognisable head of the plain key
-- (e.g. the first 8 chars "ak_abcd…") so admin UIs can render a list
-- of issued keys without ever holding the plain key itself. NEVER
-- store enough characters for an attacker to brute-force the rest of
-- the hash — 6-12 chars is the kit's recommended range.
ALTER TABLE auth_api_keys ADD COLUMN IF NOT EXISTS key_prefix text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS auth_api_keys_subject_idx ON auth_api_keys (subject);
CREATE INDEX IF NOT EXISTS auth_api_keys_expires_at_idx ON auth_api_keys (expires_at)
    WHERE expires_at IS NOT NULL AND revoked_at IS NULL;
