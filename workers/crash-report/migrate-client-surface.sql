-- Add client surface isolation to usage and aggregate metric tables.
-- Apply once before deploying a worker that writes surface-aware rows:
--   wrangler d1 execute reasonix-crash --remote --file=migrate-client-surface.sql
-- D1 imports do not support BEGIN/COMMIT wrappers. The command stops on the
-- first error but does not roll back earlier statements, so take a D1 backup
-- before applying this one-time table rebuild and do not rerun it after success.

CREATE TABLE pings_surface (
  date TEXT NOT NULL,
  surface TEXT NOT NULL DEFAULT 'desktop',
  install_id TEXT NOT NULL,
  version TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  os_version TEXT NOT NULL DEFAULT '',
  opens INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (date, surface, install_id)
);
INSERT INTO pings_surface (date, surface, install_id, version, os, arch, os_version, opens)
SELECT date, 'desktop', install_id, version, os, arch, os_version, opens FROM pings;
DROP TABLE pings;
ALTER TABLE pings_surface RENAME TO pings;

CREATE TABLE metrics_surface (
  date TEXT NOT NULL,
  surface TEXT NOT NULL DEFAULT 'desktop',
  version TEXT NOT NULL,
  os TEXT NOT NULL,
  signal TEXT NOT NULL,
  bucket TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (date, surface, version, os, signal, bucket)
);
INSERT INTO metrics_surface (date, surface, version, os, signal, bucket, count)
SELECT date, 'desktop', version, os, signal, bucket, count FROM metrics;
DROP TABLE metrics;
ALTER TABLE metrics_surface RENAME TO metrics;

CREATE TABLE metric_users_surface (
  date TEXT NOT NULL,
  surface TEXT NOT NULL DEFAULT 'desktop',
  signal TEXT NOT NULL,
  bucket TEXT NOT NULL,
  install_id TEXT NOT NULL,
  version TEXT NOT NULL,
  os TEXT NOT NULL,
  PRIMARY KEY (date, surface, signal, bucket, install_id)
);
INSERT INTO metric_users_surface (date, surface, signal, bucket, install_id, version, os)
SELECT date, 'desktop', signal, bucket, install_id, version, os FROM metric_users;
DROP TABLE metric_users;
ALTER TABLE metric_users_surface RENAME TO metric_users;

CREATE INDEX IF NOT EXISTS pings_version ON pings (surface, version);
CREATE INDEX IF NOT EXISTS metrics_signal_bucket ON metrics (surface, signal, bucket);
CREATE INDEX IF NOT EXISTS metric_users_signal_bucket ON metric_users (surface, signal, bucket);
