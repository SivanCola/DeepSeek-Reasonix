import assert from "node:assert/strict";
import { test } from "node:test";
import { loadCatalog, releaseForVersion, renderGitHubRelease, validateCatalog } from "./release-notes.mjs";
import { validateReleaseEvent } from "./release-event.mjs";
import { verifyPreviewSoak } from "./verify-preview-soak.mjs";
import { verifyReleaseCI } from "./verify-release-ci.mjs";

test("the committed release catalog is valid and newest first", async () => {
  const catalog = await loadCatalog();
  assert.equal(catalog.schemaVersion, 1);
  assert.equal(new Set(catalog.releases.map((release) => release.version)).size, catalog.releases.length);
});

test("tag namespaces resolve to the same product release", async () => {
  const catalog = await loadCatalog();
  assert.equal(releaseForVersion(catalog, "v1.17.13").version, "1.17.13");
  assert.equal(releaseForVersion(catalog, "desktop-v1.17.13").version, "1.17.13");
  assert.equal(releaseForVersion(catalog, "npm-v1.17.13").version, "1.17.13");
});

test("GitHub rendering keeps product sections and source PR links", async () => {
  const catalog = await loadCatalog();
  const markdown = renderGitHubRelease(releaseForVersion(catalog, "1.17.13"), "zh");
  assert.match(markdown, /## 使用攻略/);
  assert.match(markdown, /## 重点内容/);
  assert.match(markdown, /## 升级提醒/);
  assert.match(markdown, /## 风险提示/);
  assert.match(markdown, /## 致谢/);
  assert.match(markdown, /\/pull\/6460/);
  assert.match(markdown, /reasonix\.io\/changelog\/v1\.17\.13/);
});

test("validation rejects bilingual drift", () => {
  assert.throws(
    () =>
      validateCatalog({
        schemaVersion: 1,
        releases: [
          {
            version: "1.0.0",
            date: "2026-01-01",
            channel: "stable",
            title: { en: "Title", zh: "" },
            summary: { en: "Summary", zh: "摘要" },
          },
        ],
      }),
    /title\.zh/,
  );
});

test("managed Preview records bind every surface to one exact ordinal", () => {
  const release = {
    version: "1.19.0-preview.3",
    releaseId: "1.19.0-preview.3",
    baseVersion: "1.19.0",
    date: "2026-07-31",
    channel: "prerelease",
    status: "reviewed",
    previousRelease: "1.19.0-preview.2",
    builds: {
      cli: "v1.19.0-preview.3",
      desktop: "v1.19.0-preview.3",
      npm: "1.19.0-canary.3",
    },
    title: { en: "Preview", zh: "预览版" },
    summary: { en: "Preview summary", zh: "预览版摘要" },
    surfaces: ["cli"],
    guides: [],
    highlights: [{
      kind: "fixed",
      title: { en: "Fix", zh: "修复" },
      body: { en: "A fix.", zh: "一项修复。" },
      refs: [1],
    }],
    changes: { new: [], improved: [], fixed: [] },
    upgrade: [],
    risks: [],
    contributors: [],
    links: {
      github: "https://github.com/esengine/DeepSeek-Reasonix/releases/tag/v1.19.0-preview.3",
      compare: "https://github.com/esengine/DeepSeek-Reasonix/compare/v1.19.0-preview.2...v1.19.0-preview.3",
      download: "https://reasonix.io/?download=desktop&channel=preview#start",
    },
  };

  assert.doesNotThrow(() => validateCatalog({ schemaVersion: 1, releases: [release] }));
  assert.throws(
    () => validateCatalog({
      schemaVersion: 1,
      releases: [{ ...release, builds: { ...release.builds, npm: "1.19.0-canary.4" } }],
    }),
    /builds\.npm/,
  );
});

test("publication marker is bound to reviewed version, channel, SHA, and builds", () => {
  const release = {
    version: "1.19.0-preview.3",
    channel: "prerelease",
    candidateSha: "a".repeat(40),
    builds: {
      cli: "v1.19.0-preview.3",
      desktop: "v1.19.0-preview.3",
      npm: "1.19.0-canary.3",
    },
  };
  const event = {
    schemaVersion: 1,
    releaseId: release.version,
    channel: "preview",
    candidateSha: release.candidateSha,
    publishedAt: "2026-07-31T00:00:00.000Z",
    releaseNotesUrl: "https://reasonix.io/changelog/v1.19.0-preview.3/",
    builds: release.builds,
  };

  assert.doesNotThrow(() => validateReleaseEvent(event, release));
  assert.throws(
    () => validateReleaseEvent({ ...event, candidateSha: "b".repeat(40) }, release),
    /candidateSha/,
  );
});

test("Stable promotion metadata preserves old records and binds new records", () => {
  const base = {
    version: "2.0.0",
    releaseId: "2.0.0",
    baseVersion: "2.0.0",
    date: "2026-08-02",
    channel: "stable",
    status: "reviewed",
    previousRelease: "1.19.0",
    builds: { cli: "v2.0.0", desktop: "v2.0.0", npm: "2.0.0" },
    title: { en: "Stable", zh: "稳定版" },
    summary: { en: "Stable summary", zh: "稳定版摘要" },
    surfaces: ["cli"], guides: [],
    highlights: [{ kind: "fixed", title: { en: "Fix", zh: "修复" }, body: { en: "Fix.", zh: "修复。" }, refs: [1] }],
    changes: { new: [], improved: [], fixed: [] }, upgrade: [], risks: [], contributors: [],
    links: { github: "https://example.com/release", compare: "https://example.com/compare", download: "https://example.com/download" },
  };
  assert.doesNotThrow(() => validateCatalog({ schemaVersion: 1, releases: [base] }));
  assert.throws(
    () => validateCatalog({ schemaVersion: 1, releases: [{ ...base, promotedFrom: "2.0.0-preview.1" }] }),
    /candidateSha is required/,
  );
  const release = { ...base, promotedFrom: "2.0.0-preview.1", candidateSha: "a".repeat(40) };
  assert.doesNotThrow(() => validateCatalog({ schemaVersion: 1, releases: [release] }));
  const event = {
    schemaVersion: 1, releaseId: "2.0.0", channel: "stable", candidateSha: "a".repeat(40),
    publishedAt: "2026-08-03T00:00:00Z", releaseNotesUrl: "https://reasonix.io/changelog/v2.0.0/",
    builds: release.builds,
    promotion: {
      sourceReleaseId: "2.0.0-preview.1", sourceTag: "v2.0.0-preview.1",
      sourcePublishedAt: "2026-08-02T00:00:00Z", emergency: false,
      emergencyReasonCode: null, workflowRun: "https://github.com/example/reasonix/actions/runs/1",
    },
  };
  assert.doesNotThrow(() => validateReleaseEvent(event, release));
  assert.throws(() => validateReleaseEvent({ ...event, promotion: { ...event.promotion, sourceTag: "v2.0.0-preview.2" } }, release), /sourceTag/);
});

test("Preview soak and exact-SHA CI gates fail closed", () => {
  const publishedAt = "2026-08-01T00:00:00Z";
  assert.throws(
    () => verifyPreviewSoak({ channel: "preview", publishedAt }, { now: Date.parse("2026-08-01T23:59:59Z") }),
    /24h is required/,
  );
  assert.doesNotThrow(
    () => verifyPreviewSoak({ channel: "preview", publishedAt }, { now: Date.parse("2026-08-02T00:00:00Z") }),
  );
  assert.throws(
    () => verifyPreviewSoak({ channel: "preview", publishedAt: "2099-01-01T00:00:00Z" }, { now: Date.parse("2026-08-02T00:00:00Z") }),
    /cannot be in the future/,
  );
  assert.doesNotThrow(
    () => verifyPreviewSoak({ channel: "preview", publishedAt }, { now: Date.parse("2026-08-01T23:59:59Z"), allowEarly: true }),
  );
  const sha = "b".repeat(40);
  const run = { databaseId: 1, url: "https://github.com/example/run/1", headSha: sha, headBranch: "main-v2", event: "push", createdAt: "2026-08-02T00:00:00Z", status: "completed", conclusion: "success" };
  assert.equal(verifyReleaseCI([run], sha), run);
  assert.throws(() => verifyReleaseCI([{ ...run, conclusion: "failure" }], sha), /completed\/failure/);
  assert.throws(() => verifyReleaseCI([{ ...run, status: "in_progress", conclusion: null }], sha), /in_progress/);
  assert.throws(() => verifyReleaseCI([{ ...run, headBranch: "topic" }], sha), /no main-v2 push CI run/);
  assert.throws(() => verifyReleaseCI([], sha), /no main-v2 push CI run/);
  const olderFailure = { ...run, databaseId: 0, createdAt: "2026-08-01T00:00:00Z", conclusion: "failure" };
  const newerSuccess = { ...run, databaseId: 2, createdAt: "2026-08-03T00:00:00Z", conclusion: "success" };
  assert.equal(verifyReleaseCI([olderFailure, newerSuccess], sha), newerSuccess);
});

test("emergency promotion metadata requires a public-safe reason code", () => {
  const release = {
    version: "2.0.0",
    releaseId: "2.0.0",
    baseVersion: "2.0.0",
    date: "2026-08-02",
    channel: "stable",
    status: "reviewed",
    previousRelease: "1.19.0",
    promotedFrom: "2.0.0-preview.1",
    candidateSha: "a".repeat(40),
    builds: { cli: "v2.0.0", desktop: "v2.0.0", npm: "2.0.0" },
    title: { en: "Stable", zh: "稳定版" },
    summary: { en: "Stable summary", zh: "稳定版摘要" },
    surfaces: ["cli"], guides: [],
    highlights: [{ kind: "fixed", title: { en: "Fix", zh: "修复" }, body: { en: "Fix.", zh: "修复。" }, refs: [1] }],
    changes: { new: [], improved: [], fixed: [] }, upgrade: [], risks: [], contributors: [],
    links: { github: "https://example.com/release", compare: "https://example.com/compare", download: "https://example.com/download" },
  };
  const event = {
    schemaVersion: 1, releaseId: "2.0.0", channel: "stable", candidateSha: "a".repeat(40),
    publishedAt: "2026-08-03T00:00:00Z", releaseNotesUrl: "https://reasonix.io/changelog/v2.0.0/",
    builds: release.builds,
    promotion: {
      sourceReleaseId: "2.0.0-preview.1", sourceTag: "v2.0.0-preview.1",
      sourcePublishedAt: "2026-08-02T00:00:00Z", emergency: true,
      emergencyReasonCode: "security", workflowRun: "https://github.com/example/reasonix/actions/runs/1",
    },
  };
  assert.doesNotThrow(() => validateReleaseEvent(event, release));
  assert.throws(
    () => validateReleaseEvent({
      ...event,
      promotion: { ...event.promotion, emergencyReasonCode: "convenience" },
    }, release),
    /emergencyReasonCode/,
  );
  assert.throws(
    () => validateReleaseEvent({
      ...event,
      promotion: { ...event.promotion, emergency: false, emergencyReasonCode: "security" },
    }, release),
    /must not have an emergencyReasonCode/,
  );
});
