-- Per-device secrets held encrypted-at-rest: the material provisioning produces
-- that must survive but stay confidential - a device's LUKS recovery passphrase,
-- its break-glass local-admin password. The ciphertext is sealed by the
-- application (AES-256-GCM, secretbox); the database never sees plaintext. Keyed
-- (tenant, tag, kind) so re-imaging a device replaces its secret of that kind.
-- created_by / revealed_* record who stored and who last read it, so a reveal is
-- auditable even without a separate audit log.

CREATE TABLE IF NOT EXISTS device_secrets (
    tenant      text        NOT NULL,
    tag         text        NOT NULL,
    kind        text        NOT NULL,
    ciphertext  bytea       NOT NULL,
    created     timestamptz NOT NULL,
    created_by  text        NOT NULL DEFAULT '',
    revealed    timestamptz,
    revealed_by text        NOT NULL DEFAULT '',
    PRIMARY KEY (tenant, tag, kind)
);

-- The console lists a device's secrets on its detail / wizard view.
CREATE INDEX IF NOT EXISTS device_secrets_tag ON device_secrets (tenant, tag);
