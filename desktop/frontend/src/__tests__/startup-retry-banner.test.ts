import assert from "node:assert/strict";
import { buildSessionStatusBannerProps } from "../app-shell/chromeRegionBuilders";
import type { Meta, TabMeta } from "../lib/types";

// The startup banner reports the backend's failure. A retry error refines that
// failure for its own tab only, and disappears with the failure it refined.
type Input = Parameters<typeof buildSessionStatusBannerProps>[0];
const retries: Record<string, { busy: boolean; error?: string }> = {};
const banners = {
  reclaimSession: () => undefined,
  openTakeoverDialog: () => undefined,
  closeTakeoverDialog: () => undefined,
  openConfigFile: () => undefined,
  reloadConfigFile: () => undefined,
  showReleaseNotes: () => undefined,
  retryStartup: async () => undefined,
  openModelSettings: () => undefined,
  startupRetry: retries,
} as unknown as Input["banners"];
const base = {
  t: ((key: string) => key) as Input["t"],
  leaseBlocked: undefined,
  configWarnings: [],
  dismissConfigWarnings: () => undefined,
  updateChecksEnabled: false,
  shell: { reclaimBusyTab: null, takeoverDialogTab: null, providerSetupNeeded: false, needsOnboarding: false } as unknown as Input["shell"],
  banners,
  onboarding: {} as Input["onboarding"],
};
const tab = (id: string) => ({ id } as TabMeta);
const meta = (startupErr?: string) => ({ startupErr } as Meta);

retries.a = { busy: false, error: "retry failed: still broken" };
let props = buildSessionStatusBannerProps({ ...base, activeTab: tab("a"), meta: meta("provider construction failed") });
assert.equal(props.startupError, "retry failed: still broken", "retry error refines the reported failure");

props = buildSessionStatusBannerProps({ ...base, activeTab: tab("b"), meta: meta("provider construction failed") });
assert.equal(props.startupError, "provider construction failed", "another tab's retry error is not shown");
assert.equal(props.startupRetryBusy, false);

props = buildSessionStatusBannerProps({ ...base, activeTab: tab("a"), meta: meta(undefined) });
assert.equal(props.startupError, undefined, "a recovered tab shows no stale retry error");

retries.a = { busy: true };
props = buildSessionStatusBannerProps({ ...base, activeTab: tab("a"), meta: meta("provider construction failed") });
assert.equal(props.startupRetryBusy, true);
console.log("PASS startup retry banner follows the backend failure per tab");
