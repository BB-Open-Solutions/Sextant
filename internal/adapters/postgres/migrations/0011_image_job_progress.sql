-- Provisioning progress on image jobs: the station reports how far the current
-- step is (0..100) and a short label of what it is doing, so the console can
-- render a live progress bar and sub-step text during the batch wizard. Both
-- are advisory display state, reset per step; the authoritative lifecycle stays
-- in `status`. Added separately from the status/message path so progress ticks
-- (frequent) do not contend with status transitions (rare, guarded).

ALTER TABLE image_jobs ADD COLUMN IF NOT EXISTS progress int  NOT NULL DEFAULT 0;
ALTER TABLE image_jobs ADD COLUMN IF NOT EXISTS step     text NOT NULL DEFAULT '';
