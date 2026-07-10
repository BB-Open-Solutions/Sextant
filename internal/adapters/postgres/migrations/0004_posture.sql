-- Security posture (design 0001): Secure Boot and TPM2 LUKS state as the
-- agent observes it. Empty string = not reported (old agent / probe
-- failed), which the domain treats as "unknown".

ALTER TABLE device_status ADD COLUMN IF NOT EXISTS sb_state   text NOT NULL DEFAULT '';
ALTER TABLE device_status ADD COLUMN IF NOT EXISTS tpm2_state text NOT NULL DEFAULT '';
