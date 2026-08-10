-- Diagnostics v2: additive Windows attribution and 30-day installation counts.
-- Apply after a D1 backup and before deploying the matching Worker.

ALTER TABLE reports ADD COLUMN webview2 TEXT NOT NULL DEFAULT '';

ALTER TABLE pings ADD COLUMN os_build INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pings ADD COLUMN os_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cli_pings ADD COLUMN os_build INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cli_pings ADD COLUMN os_revision INTEGER NOT NULL DEFAULT 0;

ALTER TABLE metric_users ADD COLUMN arch TEXT NOT NULL DEFAULT '';
ALTER TABLE metric_users ADD COLUMN os_build INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_users ADD COLUMN os_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_users ADD COLUMN event_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cli_metric_users ADD COLUMN arch TEXT NOT NULL DEFAULT '';
ALTER TABLE cli_metric_users ADD COLUMN os_build INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cli_metric_users ADD COLUMN os_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cli_metric_users ADD COLUMN event_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS report_daily (
  date TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  events INTEGER NOT NULL DEFAULT 0,
  identified_events INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (date, fingerprint)
);

CREATE TABLE IF NOT EXISTS report_installations (
  date TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  install_id TEXT NOT NULL,
  version TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  os_build INTEGER NOT NULL DEFAULT 0,
  os_revision INTEGER NOT NULL DEFAULT 0,
  channel TEXT NOT NULL DEFAULT '',
  runtime_version TEXT NOT NULL DEFAULT '',
  failure_kind TEXT NOT NULL DEFAULT '',
  failure_reason TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  recovery TEXT NOT NULL DEFAULT '',
  gpu_disabled INTEGER,
  events INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (date, fingerprint, install_id)
);

CREATE INDEX IF NOT EXISTS report_installations_fingerprint_date
  ON report_installations (fingerprint, date);
