import { describe, expect, it } from "vitest";
import {
  isDevelopmentReport,
  effectiveGroupSeverity,
  isKnownNonCrashDiagnostic,
  normalizeForFingerprint,
  severityForReport,
} from "./index";
import { renderStats } from "./stats";

const base = {
  kind: "crash",
  source: "frontend.global",
  label: "window.error",
  errorType: "Error",
  errorMessage: "boom",
  topFrame: "at render (assets/index.js:1:2)",
};

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
        severity: "high",
        title: "[window.error] ResizeObserver loop limit exceeded",
        last_version: "v1.8.1",
        last_channel: "stable",
      }),
    ).toBe("low");
    expect(
      effectiveGroupSeverity({
        severity: "critical",
        title: "[window.error] ResizeObserver loop limit exceeded",
        last_version: "dev",
        last_channel: "DEV",
      }),
    ).toBe("critical");
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
        { ...row, fingerprint: "dev", title: "development-only", last_version: "dev-32bit", last_channel: "DEV", severity: "low" },
        { ...row, fingerprint: "notice", title: "browser-notice-only", severity: "low" },
      ],
      metrics: [],
      previousMetrics: [],
      metricUsers: [],
      sources: [],
      overview: { latestAdoptionPct: null, openReports: 4, newLatestReports: 0, regressedReports: 0, criticalOpenReports: 1 },
      latestVersion: "v1.40.0",
      filters: {
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
});
