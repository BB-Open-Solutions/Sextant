-- #27: a user asking an administrator for permission to do one privileged
-- thing on their own machine, and the answer.
--
-- Short-lived by nature: a request is dead five minutes after it is created
-- (elevation.TTL). Rows are kept a little beyond that so the console can show
-- what happened and an auditor can see who approved what, then pruned - this
-- table must never become a log.
--
-- No state column. Pending-versus-expired is a function of the clock, and a
-- stored state that depends on a timer is a state that lies after a restart:
-- whatever process was going to write "expired" may not have been running.
-- decided_at IS NULL means nobody has answered; the domain works out the rest.
CREATE TABLE IF NOT EXISTS elevation_requests (
    tenant     text NOT NULL DEFAULT '',
    id         text NOT NULL,
    tag        text NOT NULL,
    "user"     text NOT NULL,
    action     text NOT NULL DEFAULT '',
    reason     text NOT NULL DEFAULT '',
    approved   boolean,
    created    timestamptz NOT NULL,
    decided_at timestamptz,
    decided_by text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant, id)
);

-- The operator's queue reads unanswered requests newest-first per tenant; the
-- pruner deletes by age. Both are covered by ordering on created.
CREATE INDEX IF NOT EXISTS elevation_requests_open
    ON elevation_requests (tenant, created)
    WHERE decided_at IS NULL;
