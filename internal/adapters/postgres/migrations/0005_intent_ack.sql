-- Remote-action acknowledgement (design 0004): the last intent a device
-- confirmed executing, so the console shows armed vs delivered.

ALTER TABLE device_status ADD COLUMN IF NOT EXISTS ack text NOT NULL DEFAULT '';
