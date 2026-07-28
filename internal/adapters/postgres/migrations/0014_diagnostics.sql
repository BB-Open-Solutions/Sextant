-- design 0010: at most one sealed diagnostics bundle per device. Short-lived
-- support material (application enforces retention), sealed by the
-- application (AES-256-GCM) - the store never holds plaintext logs.
CREATE TABLE IF NOT EXISTS device_diagnostics (
    tenant     text NOT NULL DEFAULT '',
    tag        text NOT NULL,
    ciphertext bytea NOT NULL,
    created    timestamptz NOT NULL,
    PRIMARY KEY (tenant, tag)
);
