import { describe, expect, it } from "vitest";
// @ts-expect-error Node 22+ provides node:sqlite; Worker production code does not import it.
import { DatabaseSync } from "node:sqlite";
import worker, {
  groupFingerprintFromPath,
  isDevelopmentReport,
  effectiveGroupSeverity,
  isDevelopmentGroup,
  isKnownNonCrashDiagnostic,
  namespaceReportFingerprint,
  newestReleaseVersion,
  diagnosticWindowWhere,
  normalizeForFingerprint,
  Ping,
  Report,
  Metrics,
  CLI_TELEMETRY_SCHEMA_SQL,
  ensureCLITelemetrySchema,
  refreshMetricUserRollup,
  severityForReport,
  telemetryTableNames,
} from "./index";
import { crashGroups } from "./diagnostics_v2";
import type { Env } from "./env";
import { renderStats } from "./stats";
import clientSurfaceMigrationSQL from "../migrate-client-surface.sql?raw";
import diagnosticsMigrationSQL from "../migrate-diagnostics-v2.sql?raw";
import freshSchemaSQL from "../schema.sql?raw";

const base = {
  kind: "crash",
  source: "frontend.global",
  label: "window.error",
  errorType: "Error",
  errorMessage: "boom",
  topFrame: "at render (assets/index.js:1:2)",
};

describe("metrics compatibility", () => {
  it("defaults old ping and metrics payloads to desktop", () => {
    const ping = Ping.parse({
      installId: "a".repeat(32),
      version: "v1.20.0",
      os: "darwin",
      arch: "arm64",
    });
    const metrics = Metrics.parse({
      version: "v1.20.0",
      os: "darwin",
      counters: [{ signal: "turns", bucket: "count", count: 1 }],
    });
    expect(ping.surface).toBe("desktop");
    expect(metrics.surface).toBe("desktop");
  });

  it("accepts CLI surface and fixed CLI signals", () => {
    const parsed = Metrics.safeParse({
      surface: "cli",
      version: "v1.20.0",
      os: "linux",
      counters: [
        { signal: "cli_mode", bucket: "run", count: 1 },
        { signal: "cli_profile", bucket: "delivery", count: 1 },
        { signal: "cli_turn_latency", bucket: "s_5_15", count: 1 },
        { signal: "cli_exit", bucket: "success", count: 1 },
      ],
    });
    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.surface).toBe("cli");
  });

  it("rejects invalid client surfaces", () => {
    expect(
      Metrics.safeParse({
        surface: "server",
        version: "v1.20.0",
        os: "linux",
        counters: [{ signal: "turns", bucket: "count", count: 1 }],
      }).success,
    ).toBe(false);
  });

  it("drops unknown signals without rejecting known counters in the batch", () => {
    const payload = {
      version: "v1.17.16",
      os: "darwin",
      counters: [
        { signal: "settings_auto_plan", bucket: "off", count: 1 },
        { signal: "cache_hit", bucket: "90_100", count: 1 },
      ],
    };

    const parsed = Metrics.safeParse(payload);
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.counters).toEqual([{ signal: "cache_hit", bucket: "90_100", count: 1 }]);
  });

  it("accepts an all-unknown batch as an empty no-op", () => {
    const parsed = Metrics.safeParse({
      version: "v1.18.0",
      os: "darwin",
      counters: [{ signal: "future_signal", arbitrary: "future payload" }],
    });

    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.counters).toEqual([]);
  });

  it("still rejects malformed counters for known signals", () => {
    expect(
      Metrics.safeParse({
        version: "v1.18.0",
        os: "darwin",
        counters: [{ signal: "cache_hit", bucket: "not allowed", count: 1 }],
      }).success,
    ).toBe(false);
  });

  it("accepts desktop lifecycle and Windows diagnostic counters", () => {
    const parsed = Metrics.safeParse({
      version: "v1.19.0",
      os: "windows",
      counters: [
        { signal: "desktop_exit_phase", bucket: "healthy", count: 2 },
        { signal: "desktop_uptime", bucket: "m_2_10", count: 2 },
        { signal: "desktop_webview2_failure", bucket: "gpu_process_exited", count: 1 },
        { signal: "desktop_restore", bucket: "timeout", count: 1 },
      ],
    });

    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.counters).toHaveLength(4);
  });
});

describe("diagnostics v2 compatibility and privacy", () => {
  const oldReport = {
    kind: "crash",
    version: "v1.19.0",
    os: "windows",
    arch: "amd64",
    message: "legacy payload",
  } as const;

  it("accepts old reports and additive Windows diagnostics", () => {
    expect(Report.safeParse(oldReport).success).toBe(true);
    const parsed = Report.safeParse({
      ...oldReport,
      installId: "b".repeat(32),
      channel: "stable",
      device: { osVersion: "Windows 10", osBuild: 17763, osRevision: 6293 },
      webview2: {
        kind: "browser_process_exited",
        reason: "integrity_failure",
        exitCode: -1073740760,
        processDescription: "Browser",
        failureSourceModule: "inject.dll",
        runtimeVersion: "132.0.2957.140",
        gpuDisabled: false,
        recovery: "not_applicable",
      },
    });
    expect(parsed.success).toBe(true);
  });

  it("rejects full failure-source paths and puts channel=test in development", () => {
    expect(Report.safeParse({
      ...oldReport,
      webview2: {
        kind: "browser_process_exited",
        reason: "integrity_failure",
        failureSourceModule: "C:\\Users\\alice\\inject.dll",
        runtimeVersion: "132",
        gpuDisabled: false,
        recovery: "not_applicable",
      },
    }).success).toBe(false);
    expect(isDevelopmentReport({ ...base, version: "v1.23.0", channel: "test" })).toBe(true);
  });

  it("keeps fresh, migrated, and runtime-bootstrap schemas aligned", () => {
    const legacy = `
      CREATE TABLE reports (id INTEGER PRIMARY KEY);
      CREATE TABLE pings (
        date TEXT NOT NULL, install_id TEXT NOT NULL, version TEXT NOT NULL, os TEXT NOT NULL,
        arch TEXT NOT NULL, os_version TEXT NOT NULL DEFAULT '', opens INTEGER NOT NULL DEFAULT 1,
        PRIMARY KEY (date, install_id)
      );
      CREATE TABLE cli_pings (
        date TEXT NOT NULL, install_id TEXT NOT NULL, version TEXT NOT NULL, os TEXT NOT NULL,
        arch TEXT NOT NULL, os_version TEXT NOT NULL DEFAULT '', opens INTEGER NOT NULL DEFAULT 1,
        PRIMARY KEY (date, install_id)
      );
      CREATE TABLE metric_users (
        date TEXT NOT NULL, signal TEXT NOT NULL, bucket TEXT NOT NULL, install_id TEXT NOT NULL,
        version TEXT NOT NULL, os TEXT NOT NULL, PRIMARY KEY (date, signal, bucket, install_id)
      );
      CREATE TABLE cli_metric_users (
        date TEXT NOT NULL, signal TEXT NOT NULL, bucket TEXT NOT NULL, install_id TEXT NOT NULL,
        version TEXT NOT NULL, os TEXT NOT NULL, PRIMARY KEY (date, signal, bucket, install_id)
      );
    `;
    const columns = (db: DatabaseSync, table: string) =>
      db.prepare(`PRAGMA table_info(${table})`).all().map((row: Record<string, unknown>) => String(row.name));

    const fresh = new DatabaseSync(":memory:");
    const migrated = new DatabaseSync(":memory:");
    const runtimeBootstrap = new DatabaseSync(":memory:");
    try {
      fresh.exec(freshSchemaSQL);
      migrated.exec(legacy);
      migrated.exec(diagnosticsMigrationSQL);
      runtimeBootstrap.exec(CLI_TELEMETRY_SCHEMA_SQL.join(";\n"));

      const additiveColumns: Record<string, string[]> = {
        reports: ["webview2"],
        pings: ["os_build", "os_revision"],
        cli_pings: ["os_build", "os_revision"],
        metric_users: ["arch", "os_build", "os_revision", "event_count"],
        cli_metric_users: ["arch", "os_build", "os_revision", "event_count"],
      };
      for (const [table, expected] of Object.entries(additiveColumns)) {
        expect(columns(migrated, table)).toEqual(expect.arrayContaining(expected));
        expect(columns(fresh, table)).toEqual(expect.arrayContaining(expected));
      }
      for (const table of ["report_daily", "report_installations"]) {
        expect(columns(migrated, table)).toEqual(columns(fresh, table));
      }
      for (const table of ["cli_pings", "cli_metric_users"]) {
        expect(columns(runtimeBootstrap, table)).toEqual(columns(fresh, table));
      }

      runtimeBootstrap.exec(`
        INSERT INTO cli_pings
          (date, install_id, version, os, arch, os_version, os_build, os_revision, opens)
        VALUES
          (date('now'), '0123456789abcdef0123456789abcdef', 'v1', 'windows', 'amd64', '10', 17763, 1, 1);
        INSERT INTO cli_metric_users
          (date, version, os, arch, os_build, os_revision, signal, bucket, install_id, event_count)
        VALUES
          (date('now'), 'v1', 'windows', 'amd64', 17763, 1, 'turns', 'count',
           '0123456789abcdef0123456789abcdef', 1);
      `);
    } finally {
      fresh.close();
      migrated.close();
      runtimeBootstrap.close();
    }
    expect(diagnosticsMigrationSQL).not.toMatch(/\bDROP\b/);
  });
});

describe("stats window and release baseline", () => {
  it("uses an inclusive calendar window for diagnostic groups", () => {
    expect(diagnosticWindowWhere(7)).toBe("date(last_seen) >= date('now', '-6 day')");
    expect(diagnosticWindowWhere(30)).toBe("date(last_seen) >= date('now', '-29 day')");
  });

  it("does not promote prerelease or synthetic non-semver labels", () => {
    expect(newestReleaseVersion(["v1.19.4", "v1.20.0-beta.1", "dev", "v9.9.9-test"])).toBe("v1.19.4");
  });
});

describe("diagnostics v2 storage consistency", () => {
  it("commits every report write through one D1 batch", async () => {
    let batchCalls = 0;
    let directRuns = 0;
    let committed: Array<{ sql: string }> = [];
    const db = {
      prepare(sql: string) {
        const statement = {
          sql,
          bind() {
            return statement;
          },
          async first() {
            return null;
          },
          async run() {
            directRuns++;
            return {};
          },
        };
        return statement;
      },
      async batch(statements: Array<{ sql: string }>) {
        batchCalls++;
        committed = statements;
        return [];
      },
    } as unknown as D1Database;
    const env = {
      DB: db,
      RATE_LIMITER: { async limit() { return { success: true }; } },
    } as unknown as Env;

    const body = JSON.stringify({
      installId: "a".repeat(32),
      kind: "crash",
      version: "v1.23.0",
      os: "windows",
      arch: "amd64",
      message: "browser process exited",
    });
    const response = await worker.fetch(
      new Request("https://crash.reasonix.io/v1/report", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "content-length": String(new TextEncoder().encode(body).byteLength),
          "cf-connecting-ip": "127.0.0.1",
        },
        body,
      }),
      env,
    );

    expect(response.status).toBe(202);
    expect(batchCalls).toBe(1);
    expect(directRuns).toBe(0);
    expect(committed).toHaveLength(5);
    expect(committed.map((statement) => statement.sql)).toEqual([
      expect.stringContaining("INSERT INTO groups"),
      expect.stringContaining("INSERT INTO reports"),
      expect.stringContaining("INSERT INTO report_daily"),
      expect.stringContaining("INSERT INTO report_installations"),
      expect.stringContaining("DELETE FROM reports"),
    ]);
  });

  it("orders the SQL limit and returned groups by affected installations first", async () => {
    let querySQL = "";
    const row = (fingerprint: string, severity: string, affectedInstalls: number) => ({
      fingerprint,
      status: "open",
      severity,
      regressed_at: "",
      first_version: "v1.23.0",
      count: 100,
      seen: "2026-08-10",
      title: "browser process exited",
      last_version: "v1.23.0",
      last_channel: "stable",
      affected_installs: affectedInstalls,
      window_events: 100,
      identified_events: 100,
      active_build_installs: 0,
      kind: "crash",
      source: "desktop.webview2",
      label: "browser_process_exited",
      error_type: "",
      top_frame: "",
      last_os: "windows",
      last_arch: "amd64",
    });
    const db = {
      prepare(sql: string) {
        querySQL = sql;
        return {
          async all() {
            return { results: [row("b".repeat(64), "critical", 1), row("a".repeat(64), "low", 20)] };
          },
        };
      },
    } as unknown as D1Database;
    const env = { DB: db } as unknown as Env;

    const result = await crashGroups(env, {
      status: "",
      source: "",
      version: "",
      os: "",
      platform: "",
      osBuild: "",
      arch: "",
      channel: "",
      runtimeVersion: "",
      failureKind: "",
      failureReason: "",
      recovery: "",
      gpu: "",
      newLatest: false,
      regressed: false,
      windowDays: 7,
    }, "");

    expect(querySQL.indexOf("affected_installs DESC")).toBeLessThan(
      querySQL.indexOf("CASE WHEN status = 'open'"),
    );
    expect(result.results.map((group) => group.fingerprint)).toEqual([
      "a".repeat(64),
      "b".repeat(64),
    ]);
  });
});

describe("telemetry deployment order compatibility", () => {
  it("keeps the released Desktop tables unchanged and isolates CLI rows", () => {
    expect(telemetryTableNames("desktop")).toEqual({
      pings: "pings",
      metrics: "metrics",
      metricUsers: "metric_users",
    });
    expect(telemetryTableNames("cli")).toEqual({
      pings: "cli_pings",
      metrics: "cli_metrics",
      metricUsers: "cli_metric_users",
    });
  });

  it("keeps the migration additive when it runs before the released Worker", () => {
    expect(clientSurfaceMigrationSQL).not.toMatch(/\b(?:DROP|ALTER)\b/);
    expect(clientSurfaceMigrationSQL).not.toMatch(
      /CREATE TABLE(?: IF NOT EXISTS)?\s+(?:pings|metrics|metric_users)\b/,
    );
    for (const table of ["cli_pings", "cli_metrics", "cli_metric_users"]) {
      expect(clientSurfaceMigrationSQL).toMatch(
        new RegExp(`CREATE TABLE IF NOT EXISTS\\s+${table}\\b`),
      );
    }
  });

  it("extends the legacy CLI migration with the diagnostics bootstrap columns", () => {
    const runtimeSchema = CLI_TELEMETRY_SCHEMA_SQL.join("\n");
    expect(runtimeSchema).toContain("os_build INTEGER NOT NULL DEFAULT 0");
    expect(runtimeSchema).toContain("os_revision INTEGER NOT NULL DEFAULT 0");
    expect(runtimeSchema).toContain("arch TEXT NOT NULL DEFAULT ''");
    expect(runtimeSchema).toContain("event_count INTEGER NOT NULL DEFAULT 0");
  });

  it("uses additive idempotent DDL when the Worker deploys before the migration", async () => {
    const prepared: string[] = [];
    let batches = 0;
    const db = {
      prepare(sql: string) {
        prepared.push(sql);
        return { sql };
      },
      async batch() {
        batches++;
        return [];
      },
    } as unknown as D1Database;

    await Promise.all([
      ensureCLITelemetrySchema({ DB: db }),
      ensureCLITelemetrySchema({ DB: db }),
    ]);

    expect(batches).toBe(1);
    expect(prepared).toEqual([...CLI_TELEMETRY_SCHEMA_SQL]);
    expect(prepared.every((sql) => /CREATE (?:TABLE|INDEX) IF NOT EXISTS/.test(sql))).toBe(true);
    expect(prepared.join("\n")).not.toMatch(/\b(?:DROP|ALTER)\b/);
  });

  it("retries schema initialization after a transient D1 failure", async () => {
    let batches = 0;
    const db = {
      prepare(sql: string) {
        return { sql };
      },
      async batch() {
        batches++;
        if (batches === 1) throw new Error("temporary D1 failure");
        return [];
      },
    } as unknown as D1Database;

    await expect(ensureCLITelemetrySchema({ DB: db })).rejects.toThrow("temporary D1 failure");
    await expect(ensureCLITelemetrySchema({ DB: db })).resolves.toBeUndefined();
    expect(batches).toBe(2);
  });
});

function fakeRollupDB(options: { failAtQuery?: number } = {}) {
  const cursor = { next_signal: 0 };
  const queried: string[] = [];
  const batches: string[][] = [];
  const db = {
    prepare(sql: string) {
      const stmt = {
        sql,
        binds: [] as unknown[],
        bind(...args: unknown[]) {
          stmt.binds = args;
          return stmt;
        },
        async first() {
          return sql.includes("FROM metric_user_rollup_state") ? { next_signal: cursor.next_signal } : null;
        },
        async all() {
          if (!sql.includes("COUNT(DISTINCT install_id)")) return { results: [] };
          const nth = queried.length;
          queried.push(String(stmt.binds[0]));
          if (options.failAtQuery === nth) throw new Error("D1 DB exceeded its CPU time limit and was reset");
          return { results: [{ bucket: "dark", total: 7 }] };
        },
        async run() {
          if (sql.includes("INSERT INTO metric_user_rollup_state")) cursor.next_signal = Number(stmt.binds[0]);
          return {};
        },
      };
      return stmt;
    },
    async batch(stmts: { sql: string }[]) {
      batches.push(stmts.map((s) => s.sql));
      return [];
    },
  } as unknown as D1Database;
  return { env: { DB: db } as unknown as Parameters<typeof refreshMetricUserRollup>[0], cursor, queried, batches };
}

describe("metric_user rollup", () => {
  it("walks the whole signal list across runs without repeating one", async () => {
    const { env, cursor, queried } = fakeRollupDB();

    await refreshMetricUserRollup(env, 3);
    expect(cursor.next_signal).toBe(3);
    await refreshMetricUserRollup(env, 3);
    expect(cursor.next_signal).toBe(6);

    expect(queried).toHaveLength(6);
    expect(new Set(queried).size).toBe(6);
  });

  it("wraps the cursor back to the start after a full pass", async () => {
    const { env, cursor, queried } = fakeRollupDB();
    await refreshMetricUserRollup(env, 10_000);
    expect(queried.length).toBeGreaterThan(50);
    expect(new Set(queried).size).toBe(queried.length);
    expect(cursor.next_signal).toBe(0);
  });

  it("moves past a signal whose query is abandoned instead of retrying it forever", async () => {
    const { env, cursor, queried } = fakeRollupDB({ failAtQuery: 1 });
    await refreshMetricUserRollup(env, 3);
    expect(queried).toHaveLength(3);
    expect(cursor.next_signal).toBe(3);
  });

  it("replaces a signal's rows in one batch so no reader sees it half-written", async () => {
    const { env, batches } = fakeRollupDB();
    await refreshMetricUserRollup(env, 2);

    const writes = batches.filter((b) => b.some((sql) => /DELETE FROM metric_user_rollup\b/.test(sql)));
    expect(writes).toHaveLength(2);
    for (const batch of writes) {
      expect(batch[0]).toMatch(/DELETE FROM metric_user_rollup\b/);
      expect(batch.slice(1).every((sql) => /INSERT INTO metric_user_rollup\b/.test(sql))).toBe(true);
    }
  });
});

describe("diagnostic classification", () => {
  it("keeps development reports out of release crash priority", () => {
    expect(isDevelopmentReport({ ...base, version: "dev-32bit" })).toBe(true);
    expect(isDevelopmentReport({ ...base, version: "v1.40.0", channel: "dev" })).toBe(true);
    expect(severityForReport({ ...base, version: "dev" })).toBe("low");
  });

  it("downranks browser notices and recovered React renders", () => {
    expect(
      isKnownNonCrashDiagnostic({ ...base, errorMessage: "ResizeObserver loop limit exceeded" }),
    ).toBe(true);
    expect(
      isKnownNonCrashDiagnostic({ ...base, errorMessage: "Minified React error #520; recovered" }),
    ).toBe(true);
    expect(
      severityForReport({ ...base, errorMessage: "additional File object is not a file on the disk" }),
    ).toBe("low");
  });

  it("keeps actionable release crashes high", () => {
    expect(severityForReport({ ...base, version: "v1.40.0", channel: "stable" })).toBe("high");
  });

  it("reclassifies historical groups before dashboard prioritization", () => {
    expect(
      effectiveGroupSeverity({
        fingerprint: "a".repeat(64),
        severity: "high",
        title: "[window.error] ResizeObserver loop limit exceeded",
      }),
    ).toBe("low");
    expect(
      effectiveGroupSeverity({
        fingerprint: `dev:${"b".repeat(64)}`,
        severity: "critical",
        title: "[window.error] ResizeObserver loop limit exceeded",
      }),
    ).toBe("critical");
  });

  it("keeps ambiguous legacy history out of the development-only lane", () => {
    const fingerprint = "c".repeat(64);
    const stableThenDevelopment = {
      fingerprint,
      severity: "high",
      title: "[window.error] actionable release crash",
      first_version: "v1.17.15",
      last_version: "dev-32bit",
      last_channel: "dev",
    };
    const developmentThenStable = {
      ...stableThenDevelopment,
      first_version: "dev-32bit",
      last_version: "v1.17.15",
      last_channel: "stable",
    };
    // A retained first/last summary cannot distinguish a dev-only group from
    // dev -> stable -> dev once the middle release sample has been pruned.
    const developmentAroundStable = {
      ...stableThenDevelopment,
      first_version: "dev-32bit",
      last_version: "dev-32bit",
      last_channel: "dev",
    };
    expect(isDevelopmentGroup(stableThenDevelopment)).toBe(false);
    expect(effectiveGroupSeverity(stableThenDevelopment)).toBe("high");
    expect(isDevelopmentGroup(developmentThenStable)).toBe(false);
    expect(effectiveGroupSeverity(developmentThenStable)).toBe("high");
    expect(isDevelopmentGroup(developmentAroundStable)).toBe(false);
    expect(effectiveGroupSeverity(developmentAroundStable)).toBe("high");
  });
});

describe("development fingerprint namespace", () => {
  const hash = "d".repeat(64);

  it("preserves stable fingerprints and isolates development reports", () => {
    expect(namespaceReportFingerprint(hash, false)).toBe(hash);
    expect(namespaceReportFingerprint(hash, true)).toBe(`dev:${hash}`);
  });

  it("recognizes namespaced development groups independently of version labels", () => {
    expect(
      isDevelopmentGroup({
        fingerprint: `dev:${hash}`,
      }),
    ).toBe(true);
  });

  it("keeps namespaced fingerprints reachable from dashboard links", () => {
    expect(groupFingerprintFromPath(`/stats/group/dev:${hash}`)).toBe(`dev:${hash}`);
    expect(groupFingerprintFromPath(`/stats/group/${hash}`)).toBe(hash);
    expect(groupFingerprintFromPath("/stats/group/dev:not-a-hash")).toBe(null);
  });
});

describe("opaque crash fingerprints", () => {
  const opaque = {
    kind: "crash",
    source: "frontend.global",
    label: "window.error",
    errorType: "string",
    errorMessage: "Script error.",
    message: "[window.error]\n\nScript error.",
    topFrame: "",
  };

  it("splits locationless Script error reports by safe context hint", () => {
    const startup = normalizeForFingerprint({ ...opaque, fingerprintHint: "build:abc|view:app://reasonix/|cats:startup>tabs" });
    const markdown = normalizeForFingerprint({ ...opaque, fingerprintHint: "build:abc|view:app://reasonix/|cats:render>markdown" });
    expect(startup).not.toBe(markdown);
  });

  it("preserves grouping when old clients omit the optional hint", () => {
    expect(normalizeForFingerprint(opaque)).toBe(normalizeForFingerprint({ ...opaque, fingerprintHint: "" }));
    expect(normalizeForFingerprint(opaque)).toBe(
      "crash\nfrontend.global\nwindow.error\nstring\n\nScript error.",
    );
  });
});

describe("diagnostics dashboard lanes", () => {
  it("keeps release, performance, development, and notices out of one another's priority lists", () => {
    type StatsData = Parameters<typeof renderStats>[0];
    const row = {
      fingerprint: "fingerprint",
      kind: "crash",
      count: 1,
      first_version: "v1.40.0",
      last_version: "v1.40.0",
      seen: "2026-07-19",
      status: "open",
      title: "release-actionable",
      source: "frontend.global",
      label: "window.error",
      error_type: "Error",
      top_frame: "at render",
      severity: "high",
      last_os: "windows",
      last_arch: "amd64",
      last_channel: "stable",
      regressed_at: "",
    };
    const data: StatsData = {
      daily: [],
      versions: [],
      platforms: [],
      crashes: [
        row,
        { ...row, fingerprint: "perf", kind: "performance", title: "performance-only", severity: "medium" },
        {
          ...row,
          fingerprint: `dev:${"e".repeat(64)}`,
          title: "development-only",
          last_version: "dev-32bit",
          last_channel: "DEV",
          severity: "low",
          development: true,
        },
        { ...row, fingerprint: "notice", title: "browser-notice-only", severity: "low" },
      ],
      metrics: [],
      previousMetrics: [],
      metricUsers: [],
      metricUsersUnavailable: false,
      metricUsersComputedAt: "",
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 4, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 1 },
      latestVersion: "v1.40.0",
      filters: {
        surface: "desktop",
        status: "",
        source: "",
        version: "",
        os: "",
        platform: "",
        newLatest: false,
        regressed: false,
        windowDays: 30,
        preferenceMode: "users",
      },
    };

    const html = renderStats(
      data,
      { id: 1, email: "admin@example.com", role: "admin", created_at: "", approved_at: "" },
      "diagnostics",
    );
    const releaseLane = html.slice(html.indexOf("Needs attention"), html.indexOf("Performance signals"));
    const performanceLane = html.slice(html.indexOf("Performance signals"), html.indexOf("Development diagnostics"));
    const developmentLane = html.slice(html.indexOf("Development diagnostics"), html.indexOf("Report filters"));

    expect(releaseLane).toContain("release-actionable");
    expect(releaseLane).not.toContain("performance-only");
    expect(releaseLane).not.toContain("development-only");
    expect(releaseLane).not.toContain("browser-notice-only");
    expect(performanceLane).toContain("performance-only");
    expect(performanceLane).not.toContain("development-only");
    expect(developmentLane).toContain("development-only");
  });

  it("preserves the CLI surface in dashboard navigation and filters", () => {
    type StatsData = Parameters<typeof renderStats>[0];
    const data: StatsData = {
      daily: [],
      versions: [],
      platforms: [],
      crashes: [],
      metrics: [],
      previousMetrics: [],
      metricUsers: [],
      metricUsersUnavailable: false,
      metricUsersComputedAt: "",
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 0, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 0 },
      latestVersion: "",
      filters: {
        surface: "cli",
        status: "",
        source: "",
        version: "",
        os: "",
        platform: "",
        newLatest: false,
        regressed: false,
        windowDays: 30,
        preferenceMode: "users",
      },
    };
    const html = renderStats(
      data,
      { id: 1, email: "viewer@example.com", role: "viewer", created_at: "", approved_at: "" },
      "usage",
    );
    expect(html).toContain("surface=cli");
    expect(html).toContain('aria-label="Client surface"');
    expect(html).toContain('href="/stats"');
  });

  it("says the deduplication did not finish instead of showing an empty dashboard", () => {
    type StatsData = Parameters<typeof renderStats>[0];
    const data: StatsData = {
      daily: [],
      versions: [],
      platforms: [],
      crashes: [],
      metrics: [],
      previousMetrics: [],
      metricUsers: [],
        metricUsersUnavailable: true,
      metricUsersComputedAt: "",
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 0, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 0 },
      latestVersion: "",
      filters: {
        surface: "desktop",
        status: "",
        source: "",
        version: "",
        os: "",
        platform: "",
        newLatest: false,
        regressed: false,
        windowDays: 30,
        preferenceMode: "users",
      },
    };
    const html = renderStats(
      data,
      { id: 1, email: "viewer@example.com", role: "viewer", created_at: "", approved_at: "" },
      "preferences",
    );
    const installs = html.slice(html.indexOf("Deduplicated installs"), html.indexOf("Launch/open snapshots"));
    expect(installs).toContain("deduplication");
    expect(installs).toContain("window=7d");
    expect(installs).not.toContain("No settings preference metrics yet");
  });

  it("shows how old the precomputed window is", () => {
    type StatsData = Parameters<typeof renderStats>[0];
    const data: StatsData = {
      daily: [],
      versions: [],
      platforms: [],
      crashes: [],
      metrics: [],
      previousMetrics: [],
      metricUsers: [{ signal: "settings_theme", bucket: "dark", total: 12 }],
      metricUsersUnavailable: false,
      metricUsersComputedAt: "2026-08-03T07:28:41.019Z",
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 0, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 0 },
      latestVersion: "",
      filters: {
        surface: "desktop",
        status: "",
        source: "",
        version: "",
        os: "",
        platform: "",
        newLatest: false,
        regressed: false,
        windowDays: 30,
        preferenceMode: "users",
      },
    };
    const user = { id: 1, email: "viewer@example.com", role: "viewer", created_at: "", approved_at: "" } as const;
    expect(renderStats(data, user, "preferences")).toContain("2026-08-03 07:28Z");
  });

  it("shows deduplicated affected installs on agent health", () => {
    type StatsData = Parameters<typeof renderStats>[0];
    const data: StatsData = {
      daily: [],
      versions: [],
      platforms: [],
      crashes: [],
      metrics: [{ signal: "desktop_hang", bucket: "windows_ui_thread", total: 12 }],
      previousMetrics: [],
      metricUsers: [{ signal: "desktop_hang", bucket: "windows_ui_thread", total: 3 }],
      metricUsersUnavailable: false,
      metricUsersComputedAt: "",
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 0, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 0 },
      latestVersion: "v1.19.4",
      filters: {
        surface: "desktop",
        status: "",
        source: "",
        version: "",
        os: "",
        platform: "",
        newLatest: false,
        regressed: false,
        windowDays: 7,
        preferenceMode: "users",
      },
    };
    const html = renderStats(
      data,
      { id: 1, email: "viewer@example.com", role: "viewer", created_at: "", approved_at: "" },
      "health",
    );
    const installs = html.slice(html.indexOf("Affected installs"), html.indexOf("Signal distributions"));
    expect(installs).toContain("Desktop hangs");
    expect(installs).toContain(">3<");
    expect(installs).not.toContain(">12<");
  });
});
