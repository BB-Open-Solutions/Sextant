-- In-app notifications: a message addressed to one subject (recipient) or to
-- a role/group (audience). Read state is tracked per reader in a separate
-- table, so a broadcast read by one approver stays unread for the others.

CREATE TABLE IF NOT EXISTS notifications (
    id        text        NOT NULL,
    tenant    text        NOT NULL,
    recipient text        NOT NULL DEFAULT '',
    audience  text        NOT NULL DEFAULT '',
    kind      text        NOT NULL,
    title     text        NOT NULL,
    body      text        NOT NULL DEFAULT '',
    link      text        NOT NULL DEFAULT '',
    created   timestamptz NOT NULL,
    PRIMARY KEY (tenant, id)
);

CREATE INDEX IF NOT EXISTS notifications_recipient
    ON notifications (tenant, recipient, created DESC);
CREATE INDEX IF NOT EXISTS notifications_audience
    ON notifications (tenant, audience, created DESC);

CREATE TABLE IF NOT EXISTS notification_reads (
    tenant   text        NOT NULL,
    notif_id text        NOT NULL,
    subject  text        NOT NULL,
    read_at  timestamptz NOT NULL,
    PRIMARY KEY (tenant, notif_id, subject)
);
