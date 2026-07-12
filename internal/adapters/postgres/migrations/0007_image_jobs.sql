-- Imaging-execution plane: an operator dispatches an image job for a
-- discovered device; the imaging station polls its pending jobs, runs the
-- install (nixos-anywhere), and reports progress. Console-authoritative
-- (an operator creates it), so a station's whole-set discovery report can
-- never clobber it - hence a separate table from `discovered`. Keyed by MAC
-- per station, like the discovered set it draws from.

CREATE TABLE IF NOT EXISTS image_jobs (
    tenant    text        NOT NULL,
    station   text        NOT NULL,
    mac       text        NOT NULL,
    tag       text        NOT NULL,
    hardware  text        NOT NULL,
    status    text        NOT NULL DEFAULT 'pending',
    message   text        NOT NULL DEFAULT '',
    created   timestamptz NOT NULL,
    updated   timestamptz NOT NULL,
    PRIMARY KEY (tenant, station, mac)
);

-- The station polls its pending jobs; the console lists all jobs per station.
CREATE INDEX IF NOT EXISTS image_jobs_station
    ON image_jobs (tenant, station);
