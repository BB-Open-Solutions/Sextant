-- Observed plane: device check-ins and hardware inventory.
-- device_status is the hot table (one upsert per device per minute); the
-- composite primary key (tenant, tag) is the partition key when this table
-- is range/hash-partitioned at scale.

CREATE TABLE IF NOT EXISTS device_status (
    tenant     text        NOT NULL,
    tag        text        NOT NULL,
    revision   text        NOT NULL DEFAULT '',
    phase      text        NOT NULL DEFAULT '',
    error      text        NOT NULL DEFAULT '',
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (tenant, tag)
);

-- Convergence aggregates group by (tenant, revision).
CREATE INDEX IF NOT EXISTS device_status_revision
    ON device_status (tenant, revision);

CREATE TABLE IF NOT EXISTS device_facts (
    tenant     text        NOT NULL,
    tag        text        NOT NULL,
    facts      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant, tag)
);
