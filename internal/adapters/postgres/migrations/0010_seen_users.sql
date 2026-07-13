-- Users the console has seen log in, so a notification can be delivered by
-- e-mail. Populated from the OIDC session (subject, e-mail, name, groups) on
-- each authenticated request. This is the only address book the notifier needs:
-- a recipient subject resolves to one e-mail, and an audience group resolves to
-- the e-mails of everyone seen in that group. A user who has never logged in
-- gets in-app notifications only, which is acceptable - mail follows first use.

CREATE TABLE IF NOT EXISTS seen_users (
    tenant  text        NOT NULL,
    subject text        NOT NULL,
    email   text        NOT NULL DEFAULT '',
    name    text        NOT NULL DEFAULT '',
    groups  text[]      NOT NULL DEFAULT '{}',
    seen    timestamptz NOT NULL,
    PRIMARY KEY (tenant, subject)
);

-- Audience resolution filters by group membership; index the array for it.
CREATE INDEX IF NOT EXISTS seen_users_groups ON seen_users USING gin (groups);
