-- The forge credential the console pushes with (ADR 0022). One row per tenant.
--
-- WHY THIS IS STORED AND NOT MOUNTED. Until now the credential arrived as a
-- netrc in a Kubernetes secret, which meant rotating it was a kubectl edit by
-- somebody with cluster access - so in practice it was never rotated, and at
-- bb-open it was a PERSON'S account (audit finding H2, 2026-08-06). A
-- credential an admin cannot rotate from the console is a credential that
-- stays put.
--
-- The token is sealed with the app key, never stored or logged in the clear,
-- and never read back out to the browser: the console can write a new one and
-- report who did so, but it cannot show the current one. The mounted secret
-- remains the fallback when no row exists, so existing deployments do not
-- change behaviour on upgrade.
--
-- This is the second place Sextant holds a secret value at rest (see
-- 0009_smtp.sql for the first), and for the same reason: the alternative is
-- an operator pasting it somewhere worse.

CREATE TABLE IF NOT EXISTS git_identity (
    tenant     text        NOT NULL,
    -- Host as git matches it in a netrc machine line, e.g. "forge.example.org".
    host       text        NOT NULL,
    username   text        NOT NULL,
    token_enc  bytea       NOT NULL,
    updated    timestamptz NOT NULL,
    -- Who rotated it last. Kept alongside rather than only in the audit log so
    -- the settings page can answer "whose account is this" without a join.
    updated_by text        NOT NULL DEFAULT '',
    PRIMARY KEY (tenant)
);
