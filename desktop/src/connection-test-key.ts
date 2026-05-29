import type { ConnectionTestResultEvent, ConnectionTestTarget } from "./protocol";

export type ConnectionTestOptions = {
  apiKey?: string;
  baseUrl?: string | null;
  engine?: string;
  endpoint?: string | null;
  apiKeys?: Record<string, string | undefined>;
};

export type ConnectionTestSettingsSnapshot = {
  apiKeyPrefix?: string;
  baseUrl?: string;
  webSearchEngine?: string;
  webSearchEndpoint?: string;
  webSearchApiKeys?: Record<string, string | undefined>;
};

function normalizeValue(value: string | null | undefined): string {
  const trimmed = (value ?? "").trim();
  return trimmed || "<default>";
}

function fingerprintSecret(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return `${value.length}:${(hash >>> 0).toString(36)}`;
}

function credentialKey(draft: string | undefined, savedPrefix: string | undefined): string {
  const trimmedDraft = draft?.trim();
  if (trimmedDraft) return `draft:${fingerprintSecret(trimmedDraft)}`;
  return savedPrefix ? `saved:${savedPrefix}` : "saved:<unset>";
}

export function buildConnectionTestRequestKey(
  target: ConnectionTestTarget,
  opts: ConnectionTestOptions | undefined,
  settings: ConnectionTestSettingsSnapshot | null | undefined,
): string {
  if (target === "deepseek") {
    const baseUrl = opts?.baseUrl === undefined ? settings?.baseUrl : opts.baseUrl;
    return [
      "deepseek",
      `baseUrl=${normalizeValue(baseUrl)}`,
      `credential=${credentialKey(opts?.apiKey, settings?.apiKeyPrefix)}`,
    ].join("|");
  }

  const engine = opts?.engine ?? settings?.webSearchEngine ?? "bing";
  const savedPrefix = settings?.webSearchApiKeys?.[engine];
  const draftKey = opts?.apiKeys?.[engine];
  const endpoint =
    engine === "searxng"
      ? normalizeValue(opts?.endpoint === undefined ? settings?.webSearchEndpoint : opts.endpoint)
      : "<none>";

  return [
    "webSearch",
    `engine=${engine}`,
    `endpoint=${endpoint}`,
    `credential=${credentialKey(draftKey, savedPrefix)}`,
  ].join("|");
}

export function attachConnectionTestRequestKey(
  result: ConnectionTestResultEvent,
  previous: ConnectionTestResultEvent | undefined,
): ConnectionTestResultEvent {
  if (result.requestKey || !previous?.requestKey) return result;
  return { ...result, requestKey: previous.requestKey };
}

export function matchingConnectionTestResult(
  result: ConnectionTestResultEvent | undefined,
  target: ConnectionTestTarget,
  opts: ConnectionTestOptions | undefined,
  settings: ConnectionTestSettingsSnapshot | null | undefined,
): ConnectionTestResultEvent | undefined {
  if (!result) return undefined;
  const requestKey = buildConnectionTestRequestKey(target, opts, settings);
  return result.requestKey === requestKey ? result : undefined;
}
