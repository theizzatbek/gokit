-- 0002_idempotent_links.sql
--
-- Adds the UNIQUE (user_id, original_url) constraint that backs
-- links.Service.Create's ON CONFLICT pattern. Posting the same URL
-- twice from the same user now returns the existing code instead of
-- producing duplicate rows.
--
-- The previous schema only had UNIQUE (code), so concurrent posts of
-- the same URL would race to generate distinct codes and both win —
-- leaving the user with two short codes pointing at the same target.

CREATE UNIQUE INDEX IF NOT EXISTS links_user_url_uniq
    ON links (user_id, original_url);
