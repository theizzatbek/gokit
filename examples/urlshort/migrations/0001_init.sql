CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE links (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code            text NOT NULL UNIQUE,
    original_url    text NOT NULL,
    title           text NOT NULL DEFAULT '',
    description     text NOT NULL DEFAULT '',
    image_url       text NOT NULL DEFAULT '',
    visit_count     bigint NOT NULL DEFAULT 0,
    last_visited_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX links_user_id_idx ON links(user_id);

-- Refresh-token store. DDL mirrors gokit/auth/refreshpg/schema.sql.
CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
    token_hash  bytea       PRIMARY KEY,
    family_id   uuid        NOT NULL,
    parent_hash bytea       NOT NULL,
    subject     text        NOT NULL,
    issued_at   timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at  timestamptz,
    user_agent  text        NOT NULL DEFAULT '',
    ip          inet
);

CREATE INDEX IF NOT EXISTS auth_refresh_tokens_family_id_idx ON auth_refresh_tokens (family_id);
CREATE INDEX IF NOT EXISTS auth_refresh_tokens_subject_idx   ON auth_refresh_tokens (subject);
CREATE INDEX IF NOT EXISTS auth_refresh_tokens_expires_at_idx ON auth_refresh_tokens (expires_at);
