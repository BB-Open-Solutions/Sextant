-- Live resource utilisation the agent reports at check-in: a snapshot (CPU %,
-- RAM and disk used/total), not a time series. All-zero = not reported (an older
-- agent), so the console shows "unknown" rather than 0%. Kept on device_status
-- alongside the rest of the observed beat; the upsert only overwrites when the
-- beat actually carried a reading (mem_total_mb > 0), so an old agent's empty
-- beat does not wipe the last known figures.

ALTER TABLE device_status ADD COLUMN IF NOT EXISTS cpu_pct       int NOT NULL DEFAULT 0;
ALTER TABLE device_status ADD COLUMN IF NOT EXISTS mem_used_mb   int NOT NULL DEFAULT 0;
ALTER TABLE device_status ADD COLUMN IF NOT EXISTS mem_total_mb  int NOT NULL DEFAULT 0;
ALTER TABLE device_status ADD COLUMN IF NOT EXISTS disk_used_gb  int NOT NULL DEFAULT 0;
ALTER TABLE device_status ADD COLUMN IF NOT EXISTS disk_total_gb int NOT NULL DEFAULT 0;
