export const DEFAULT_DEEPSEEK_BASE_URL = "https://api.deepseek.com";

export type DeepSeekEndpointPolicyResult =
  | {
      ok: true;
      baseUrl: string;
      isOfficialHost: boolean;
    }
  | {
      ok: false;
      message: string;
    };

export interface ResolvedDeepSeekEndpoint {
  baseUrl?: string;
  apiKey?: string;
}

export type DeepSeekConnectionTestTarget =
  | {
      ok: true;
      baseUrl: string;
      apiKey: string | undefined;
    }
  | {
      ok: false;
      message: string;
    };

function trimTrailingSlashes(value: string): string {
  let out = value;
  while (out.endsWith("/")) out = out.slice(0, -1);
  return out;
}

function isLoopbackHostname(hostname: string): boolean {
  const host = hostname.toLowerCase();
  if (host === "localhost" || host === "::1" || host === "[::1]") return true;
  const octets = host.split(".");
  return (
    octets.length === 4 &&
    octets[0] === "127" &&
    octets.every((part) => /^\d+$/.test(part) && Number(part) >= 0 && Number(part) <= 255)
  );
}

export function validateDeepSeekCredentialedEndpoint(
  rawBaseUrl: string | null | undefined,
): DeepSeekEndpointPolicyResult {
  const input = rawBaseUrl?.trim() || DEFAULT_DEEPSEEK_BASE_URL;
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    return { ok: false, message: "Invalid endpoint URL" };
  }

  if (url.username || url.password) {
    return { ok: false, message: "Endpoint URL must not include credentials" };
  }

  const localHttp = url.protocol === "http:" && isLoopbackHostname(url.hostname);
  if (url.protocol !== "https:" && !localHttp) {
    return { ok: false, message: "Endpoint must use HTTPS; HTTP is only allowed for localhost" };
  }

  return {
    ok: true,
    baseUrl: trimTrailingSlashes(url.toString()),
    isOfficialHost: url.protocol === "https:" && url.hostname.toLowerCase() === "api.deepseek.com",
  };
}

export function resolveDeepSeekConnectionTestTarget(opts: {
  requestedBaseUrl?: string | null;
  requestedApiKey?: string;
  resolvedEndpoint: ResolvedDeepSeekEndpoint;
}): DeepSeekConnectionTestTarget {
  const requested = validateDeepSeekCredentialedEndpoint(opts.requestedBaseUrl);
  if (!requested.ok) return requested;

  const resolved = validateDeepSeekCredentialedEndpoint(opts.resolvedEndpoint.baseUrl);
  const requestedKey = opts.requestedApiKey?.trim();
  const resolvedKey =
    resolved.ok && resolved.baseUrl === requested.baseUrl
      ? opts.resolvedEndpoint.apiKey?.trim()
      : undefined;

  return {
    ok: true,
    baseUrl: requested.baseUrl,
    apiKey: requestedKey || resolvedKey || undefined,
  };
}
