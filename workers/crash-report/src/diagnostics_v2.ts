import type { Env } from "./env";

export const DEVELOPMENT_FINGERPRINT_PREFIX = "dev:";
export const developmentGroupSQL = `fingerprint LIKE 'dev:%'`;

type GroupPriorityRow = {
  fingerprint: string;
  status: string;
  severity: string;
  regressed_at: string;
  first_version: string;
  count: number;
  seen: string;
  title: string;
  last_version: string;
  last_channel: string;
  affected_installs?: number;
};

export function isDevelopmentGroup(row: Pick<GroupPriorityRow, "fingerprint">): boolean {
  return row.fingerprint.startsWith(DEVELOPMENT_FINGERPRINT_PREFIX);
}

export function effectiveGroupSeverity(
  row: Pick<GroupPriorityRow, "fingerprint" | "severity" | "title">,
): string {
  if (row.severity === "critical") return row.severity;
  if (isDevelopmentGroup(row)) return "low";
  if (
    row.title === "[window.error] Script error." ||
    row.title.includes("ResizeObserver loop ") ||
    row.title.includes("Minified React error #520") ||
    row.title.includes("additional File object is not a file on the disk")
  ) {
    return "low";
  }
  return row.severity;
}

function compareDiagnosticPriority(a: GroupPriorityRow, b: GroupPriorityRow, latestVersion: string): number {
  const statusRank = (value: string) => (value === "open" ? 0 : 1);
  const severityRank = (value: string) => ({ critical: 0, high: 1, medium: 2, low: 3 })[value] ?? 4;
  return (
    statusRank(a.status) - statusRank(b.status) ||
    Number(b.affected_installs ?? 0) - Number(a.affected_installs ?? 0) ||
    severityRank(a.severity) - severityRank(b.severity) ||
    Number(b.first_version === latestVersion) - Number(a.first_version === latestVersion) ||
    Number(Boolean(b.regressed_at)) - Number(Boolean(a.regressed_at)) ||
    b.count - a.count ||
    b.seen.localeCompare(a.seen)
  );
}

export function currentWindowSince(days: 7 | 30): string {
  return `-${days - 1} day`;
}

export function diagnosticWindowWhere(days: 7 | 30): string {
  return `date(last_seen) >= date('now', '${currentWindowSince(days)}')`;
}

type DiagnosticsGroupFilters = {
  status: string;
  source: string;
  version: string;
  os: string;
  platform: string;
  osBuild: string;
  arch: string;
  channel: string;
  runtimeVersion: string;
  failureKind: string;
  failureReason: string;
  recovery: string;
  gpu: string;
  newLatest: boolean;
  regressed: boolean;
  windowDays: 7 | 30;
};

export async function crashGroups(env: Env, filters: DiagnosticsGroupFilters, latestVersion: string) {
  const where: string[] = [diagnosticWindowWhere(filters.windowDays)];
  const binds: unknown[] = [];
  const add = (sql: string, value?: unknown) => {
    where.push(sql);
    if (value !== undefined) binds.push(value);
  };
  if (filters.status) add("status = ?", filters.status);
  if (filters.source) add("source = ?", filters.source);
  const installWhere: string[] = [`date >= date('now', '${currentWindowSince(filters.windowDays)}')`];
  const installBinds: unknown[] = [];
  const addInstall = (column: string, value: unknown) => {
    installBinds.push(value);
    installWhere.push(value === null ? `${column} IS NULL` : `${column} = ?`);
    if (value === null) installBinds.pop();
  };
  if (filters.version) addInstall("version", filters.version);
  if (filters.os) add("last_os = ?", filters.os);
  if (filters.platform) add("last_os || ' ' || last_arch = ?", filters.platform);
  if (filters.osBuild) addInstall("os_build", Number(filters.osBuild));
  if (filters.arch) addInstall("arch", filters.arch);
  if (filters.channel) addInstall("channel", filters.channel);
  if (filters.runtimeVersion) addInstall("runtime_version", filters.runtimeVersion);
  if (filters.failureKind) addInstall("failure_kind", filters.failureKind);
  if (filters.failureReason) addInstall("failure_reason", filters.failureReason);
  if (filters.recovery) addInstall("recovery", filters.recovery);
  if (filters.gpu) addInstall("gpu_disabled", filters.gpu === "unknown" ? null : filters.gpu === "disabled" ? 1 : 0);
  if (installWhere.length > 1) where.push("COALESCE(installations.affected_installs, 0) > 0");
  if (filters.newLatest && latestVersion) add("first_version = ?", latestVersion);
  if (filters.regressed) where.push("regressed_at <> ''");
  let latestOrder = "";
  if (latestVersion) {
    latestOrder = `CASE WHEN first_version = ? THEN 0 ELSE 1 END,`;
    binds.push(latestVersion);
  }
  const reportWindow = currentWindowSince(filters.windowDays);
  const buildActiveInstalls = filters.osBuild
    ? `(SELECT COUNT(DISTINCT install_id) FROM pings WHERE date >= date('now', '${reportWindow}') AND os_build = ${Number(filters.osBuild)})`
    : "0";
  const sql = `SELECT groups.fingerprint, kind, count, first_version, last_version, substr(last_seen, 1, 10) AS seen,
      status, title, source, label, error_type, top_frame, severity, last_os, last_arch, last_channel, regressed_at,
      COALESCE(installations.affected_installs, 0) AS affected_installs,
      COALESCE(daily.window_events, 0) AS window_events,
      COALESCE(daily.identified_events, 0) AS identified_events,
      ${buildActiveInstalls} AS active_build_installs
    FROM groups
    LEFT JOIN (
      SELECT fingerprint, COUNT(DISTINCT install_id) AS affected_installs
      FROM report_installations WHERE ${installWhere.join(" AND ")} GROUP BY fingerprint
    ) installations ON installations.fingerprint = groups.fingerprint
    LEFT JOIN (
      SELECT fingerprint, SUM(events) AS window_events, SUM(identified_events) AS identified_events
      FROM report_daily WHERE date >= date('now', '${reportWindow}') GROUP BY fingerprint
    ) daily ON daily.fingerprint = groups.fingerprint
    ${where.length ? `WHERE ${where.join(" AND ")}` : ""}
    ORDER BY
      CASE WHEN status = 'open' THEN 0 ELSE 1 END,
      CASE
        WHEN severity = 'critical' THEN 0
        WHEN ${developmentGroupSQL}
          OR title = '[window.error] Script error.'
          OR title LIKE '%ResizeObserver loop %'
          OR title LIKE '%Minified React error #520%'
          OR title LIKE '%additional File object is not a file on the disk%'
          THEN 3
        WHEN severity = 'high' THEN 1
        WHEN severity = 'medium' THEN 2
        ELSE 3
      END,
      ${latestOrder}
      CASE WHEN regressed_at <> '' THEN 0 ELSE 1 END,
      affected_installs DESC,
      window_events DESC,
      count DESC,
      last_seen DESC
    LIMIT 50`;
  const allBinds = [...installBinds, ...binds];
  const stmt = env.DB.prepare(sql);
  const query = allBinds.length ? stmt.bind(...allBinds) : stmt;
  const result = await query.all<GroupPriorityRow & {
    kind: string;
    source: string;
    label: string;
    error_type: string;
    top_frame: string;
    last_os: string;
    last_arch: string;
    window_events: number;
    identified_events: number;
    active_build_installs: number;
  }>();
  result.results = result.results
    .map((row) => ({
      ...row,
      severity: effectiveGroupSeverity(row),
      development: isDevelopmentGroup(row),
      identity_coverage: row.window_events ? row.identified_events / row.window_events : 0,
      impact_rate: row.active_build_installs ? Number(row.affected_installs ?? 0) / row.active_build_installs : null,
    }))
    .sort((a, b) => compareDiagnosticPriority(a, b, latestVersion));
  return result;
}

type ReportAggregateInput = {
  installId?: string;
  version: string;
  os: string;
  arch: string;
  device?: { osBuild?: number; osRevision?: number };
};

type WebView2AggregateInput = {
  runtimeVersion: string;
  kind: string;
  reason: string;
  exitCode?: number;
  recovery: string;
  gpuDisabled: boolean;
};

export async function recordReportAggregates(
  db: Env["DB"],
  report: ReportAggregateInput,
  fingerprint: string,
  channel: string,
  webview2?: WebView2AggregateInput,
): Promise<void> {
  await db.prepare(
    `INSERT INTO report_daily (date, fingerprint, events, identified_events)
     VALUES (date('now'), ?1, 1, ?2)
     ON CONFLICT (date, fingerprint) DO UPDATE SET
       events = events + 1, identified_events = identified_events + ?2`,
  ).bind(fingerprint, report.installId ? 1 : 0).run();
  if (!report.installId) return;
  await db.prepare(
    `INSERT INTO report_installations (
       date, fingerprint, install_id, version, os, arch, os_build, os_revision, channel,
       runtime_version, failure_kind, failure_reason, exit_code, recovery, gpu_disabled, events
     ) VALUES (date('now'), ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, 1)
     ON CONFLICT (date, fingerprint, install_id) DO UPDATE SET
       version = ?3, os = ?4, arch = ?5, os_build = ?6, os_revision = ?7, channel = ?8,
       runtime_version = ?9, failure_kind = ?10, failure_reason = ?11, exit_code = ?12,
       recovery = ?13, gpu_disabled = ?14, events = events + 1`,
  ).bind(
    fingerprint, report.installId, report.version, report.os, report.arch,
    report.device?.osBuild ?? 0, report.device?.osRevision ?? 0, channel,
    webview2?.runtimeVersion ?? "", webview2?.kind ?? "", webview2?.reason ?? "",
    webview2?.exitCode ?? null, webview2?.recovery ?? "",
    webview2 ? (webview2.gpuDisabled ? 1 : 0) : null,
  ).run();
}
