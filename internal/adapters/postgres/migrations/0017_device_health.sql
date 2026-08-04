-- Device health: systemd's verdict on the machine, alongside the revision.
--
-- A revision says what a device MEANT to run. It is written into /etc during
-- activation, BEFORE the units start, so an activation that fails half way
-- leaves a device reporting the configuration it attempted. Measured on
-- hardware 2026-08-04: the console compared that to the ring target, found
-- them equal, and called the device on spec while directory login, endpoint
-- security and secret delivery were all dead on it.
--
-- health_state is systemd's own word ("running", "degraded", ...) and doubles
-- as the "did this agent report health at all" flag: an agent that reports
-- health always sets it, so an empty value means no reading rather than a
-- healthy machine. That distinction is why failed_units alone is not enough -
-- an empty list means "nothing failed" from a new agent and "said nothing"
-- from an old one.
ALTER TABLE device_status
  ADD COLUMN IF NOT EXISTS health_state text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS failed_units text[] NOT NULL DEFAULT '{}';
