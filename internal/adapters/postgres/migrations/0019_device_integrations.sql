-- What a device makes of the integrations the fleet turned on for it.
--
-- The console configured NetBird, Wazuh and OpenBao and could see none of
-- them. Measured on 2026-08-18: the answer to "is this laptop on the mesh"
-- came from running `ip` and `ss` on the device by hand, and a planning
-- document called two of those integrations a GAP for a fortnight after they
-- started working, because nothing in the product contradicted it.
--
-- One jsonb column rather than a column per integration: which integrations
-- exist is the overlay's decision, not the console's. A fleet that gains one
-- should be able to say so without a migration here.
--
-- NULLABLE on purpose, and that is the whole design. NULL means the device
-- never reported (an older agent, or a probe that could not run) and the
-- previous value is kept; '{}' means it reported and has nothing to say, and
-- clears the row. Without that distinction, turning an integration off would
-- leave its last known state on screen forever, and an old agent's beat would
-- erase a reading it simply does not know how to make.
ALTER TABLE device_status
  ADD COLUMN IF NOT EXISTS integrations jsonb;
