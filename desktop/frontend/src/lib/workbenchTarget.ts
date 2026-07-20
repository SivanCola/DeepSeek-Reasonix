/**
 * Workbench Target projection helpers for Local vs Remote adapters.
 * Desktop always starts Local; Remote is opt-in via reconnect / Connect.
 */

export type WorkbenchTargetKind = "local" | "ssh";

export type WorkbenchActiveTarget = {
  kind: WorkbenchTargetKind;
  hostId?: string;
  workspace?: string;
  identityGen?: number;
  requestSeq?: number;
};

export type WorkbenchRemoteHint = {
  hostId?: string;
  workspace?: string;
  label?: string;
};

export type ProviderTrustPrompt = {
  hostId: string;
  host: string;
  keyType: string;
  fingerprint: string;
  workspace: string;
  providerRefs: string[];
  warning: string;
};

function goApp(): Record<string, (...args: unknown[]) => Promise<unknown>> | undefined {
  return (typeof window !== "undefined" ? window.go?.main?.App : undefined) as
    | Record<string, (...args: unknown[]) => Promise<unknown>>
    | undefined;
}

export async function fetchActiveTarget(): Promise<WorkbenchActiveTarget> {
  const app = goApp();
  if (!app?.WorkbenchActiveTarget) return { kind: "local" };
  return (await app.WorkbenchActiveTarget()) as WorkbenchActiveTarget;
}

export async function fetchLastRemoteHint(): Promise<WorkbenchRemoteHint | null> {
  const app = goApp();
  if (!app?.WorkbenchLastRemoteHint) return null;
  const hint = (await app.WorkbenchLastRemoteHint()) as WorkbenchRemoteHint;
  if (!hint?.hostId) return null;
  return hint;
}

export async function switchToLocal(): Promise<WorkbenchActiveTarget> {
  const app = goApp();
  if (!app?.WorkbenchSwitchLocal) return { kind: "local" };
  return (await app.WorkbenchSwitchLocal()) as WorkbenchActiveTarget;
}

export async function connectRemote(hostId: string, workspace: string): Promise<void> {
  const app = goApp();
  if (!app?.WorkbenchConnectRemote) throw new Error("WorkbenchConnectRemote unavailable");
  await app.WorkbenchConnectRemote(hostId, workspace);
}

export async function disconnectRemote(): Promise<void> {
  const app = goApp();
  if (!app?.WorkbenchDisconnectRemote) return;
  await app.WorkbenchDisconnectRemote();
}

export async function remoteRequest(method: string, params: unknown = {}): Promise<unknown> {
  const app = goApp();
  if (!app?.WorkbenchRemoteRequest) throw new Error("CAPABILITY_UNAVAILABLE");
  const raw = (await app.WorkbenchRemoteRequest(method, JSON.stringify(params ?? {}))) as string;
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

export async function resolveProviderTrust(accept: boolean): Promise<void> {
  const app = goApp();
  if (!app?.WorkbenchResolveProviderTrust) return;
  await app.WorkbenchResolveProviderTrust(accept);
}

export async function pendingProviderTrust(): Promise<ProviderTrustPrompt | null> {
  const app = goApp();
  if (!app?.WorkbenchPendingProviderTrust) return null;
  const p = (await app.WorkbenchPendingProviderTrust()) as ProviderTrustPrompt | null;
  return p?.hostId ? p : null;
}
