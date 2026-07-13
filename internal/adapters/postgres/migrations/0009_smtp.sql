-- Per-tenant SMTP configuration for outbound notification e-mail. One row per
-- tenant. The password is stored EITHER as a reference name (password_ref,
-- resolved from a secret mount) OR as an encrypted blob (password_enc, sealed
-- with the app key); never as plaintext. This is the one place Sextant may
-- hold a secret value at rest, and only when an operator opts into it.

CREATE TABLE IF NOT EXISTS smtp_config (
    tenant       text        NOT NULL,
    host         text        NOT NULL,
    port         integer     NOT NULL,
    mail_from    text        NOT NULL,
    username     text        NOT NULL DEFAULT '',
    password_ref text        NOT NULL DEFAULT '',
    password_enc bytea,
    security     text        NOT NULL DEFAULT 'starttls',
    updated      timestamptz NOT NULL,
    PRIMARY KEY (tenant)
);
