-- Personal preferences: per-user presentation settings (timezone, locale).
-- Keyed by the IdP subject; these are user data, not fleet configuration,
-- so they live here and never in the overlay repo.

CREATE TABLE IF NOT EXISTS user_prefs (
    tenant   text        NOT NULL,
    subject  text        NOT NULL,
    timezone text        NOT NULL DEFAULT '',
    locale   text        NOT NULL DEFAULT '',
    updated  timestamptz NOT NULL,
    PRIMARY KEY (tenant, subject)
);
