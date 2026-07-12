-- Pre-enrollment plane: devices an imaging station (the inspoelstraat) has
-- seen over PXE but that are not yet enrolled. Keyed by MAC, not by a fleet
-- tag - the device has no tag until an operator enrolls it. This is transient
-- observed state (a report replaces the station's whole set), so it lives here
-- and never in the git config plane.

CREATE TABLE IF NOT EXISTS discovered (
    tenant     text        NOT NULL,
    station    text        NOT NULL,
    mac        text        NOT NULL,
    serial     text        NOT NULL DEFAULT '',
    vendor     text        NOT NULL DEFAULT '',
    model      text        NOT NULL DEFAULT '',
    cpu        text        NOT NULL DEFAULT '',
    cores      integer     NOT NULL DEFAULT 0,
    mem_gb     integer     NOT NULL DEFAULT 0,
    disk_gb    integer     NOT NULL DEFAULT 0,
    firmware   text        NOT NULL DEFAULT '',
    facter     jsonb,
    phase      text        NOT NULL,
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (tenant, station, mac)
);

-- The console lists a station's current set; index the lookup key.
CREATE INDEX IF NOT EXISTS discovered_station
    ON discovered (tenant, station);
