-- API credentials (ADR 0008): personal tokens and service accounts.
-- The secret is never stored - only its argon2id hash. Lookups are by
-- id (the token carries its id prefix); the hash is verified in the app.

CREATE TABLE IF NOT EXISTS api_tokens (
    id        text        NOT NULL PRIMARY KEY,
    name      text        NOT NULL,
    kind      text        NOT NULL,
    subject   text        NOT NULL,
    groups    jsonb       NOT NULL DEFAULT '[]',
    ceiling   text        NOT NULL DEFAULT '',
    hash      text        NOT NULL,
    created   timestamptz NOT NULL,
    expires   timestamptz NOT NULL,
    last_used timestamptz
);

-- List a user's own tokens efficiently.
CREATE INDEX IF NOT EXISTS api_tokens_subject ON api_tokens (subject);
