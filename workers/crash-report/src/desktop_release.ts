const R2_BASE = "https://dl.reasonix.io";
const GITHUB_RELEASES_API = "https://api.github.com/repos/esengine/DeepSeek-Reasonix/releases?per_page=100";

type PublicReleaseChannel = "stable" | "preview";
type ReleaseChannel = PublicReleaseChannel | "canary";

type GitHubAsset = {
  name?: string;
  browser_download_url?: string;
  size?: number;
};

type GitHubRelease = {
  tag_name?: string;
  draft?: boolean;
  prerelease?: boolean;
  html_url?: string;
  assets?: GitHubAsset[];
};

const CLI_ASSETS = [
  "reasonix-darwin-amd64.tar.gz",
  "reasonix-darwin-arm64.tar.gz",
  "reasonix-linux-amd64.tar.gz",
  "reasonix-linux-arm64.tar.gz",
  "reasonix-windows-amd64.zip",
  "reasonix-windows-arm64.zip",
  "SHA256SUMS",
] as const;
const STABLE_CLI_TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const PREVIEW_CLI_TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-preview\.(0|[1-9]\d*)$/;

function manifestPointer(channel: ReleaseChannel): string {
  if (channel === "stable") return `${R2_BASE}/latest/latest.json`;
  return channel === "preview" ? `${R2_BASE}/preview/latest.json` : `${R2_BASE}/canary/latest.json`;
}

function gatewayHeaders(source: string): HeadersInit {
  return {
    "content-type": "application/json; charset=utf-8",
    "cache-control": "public, max-age=300, stale-if-error=86400",
    "access-control-allow-origin": "*",
    "access-control-allow-methods": "GET, HEAD, OPTIONS",
    "x-reasonix-release-source": source,
  };
}

function safeHTTPSURL(value: unknown): string {
  try {
    const url = new URL(String(value || ""));
    return url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}

function cliTagOrder(tag: unknown, channel: PublicReleaseChannel): number[] | null {
  const value = String(tag || "");
  const match = value.match(channel === "preview" ? PREVIEW_CLI_TAG : STABLE_CLI_TAG);
  return match ? match.slice(1).map(Number) : null;
}

function compareOrder(left: number[], right: number[]): number {
  for (let index = 0; index < Math.max(left.length, right.length); index += 1) {
    const difference = (left[index] || 0) - (right[index] || 0);
    if (difference !== 0) return difference;
  }
  return 0;
}

function normalizeCLIRelease(
  release: GitHubRelease,
  channel: PublicReleaseChannel,
): { release: GitHubRelease; order: number[] } | null {
  const order = cliTagOrder(release.tag_name, channel);
  if (!order || release.draft || Boolean(release.prerelease) !== (channel === "preview")) return null;

  const assetsByName = new Map<string, GitHubAsset>();
  for (const asset of Array.isArray(release.assets) ? release.assets : []) {
    const name = String(asset?.name || "");
    const download = safeHTTPSURL(asset?.browser_download_url);
    if (name && download) {
      assetsByName.set(name, { name, browser_download_url: download, size: Number(asset?.size) || 0 });
    }
  }
  if (CLI_ASSETS.some((name) => !assetsByName.has(name))) return null;

  const tag = String(release.tag_name);
  return {
    order,
    release: {
      tag_name: tag,
      prerelease: channel === "preview",
      html_url: safeHTTPSURL(release.html_url) || `https://github.com/esengine/DeepSeek-Reasonix/releases/tag/${tag}`,
      assets: CLI_ASSETS.map((name) => assetsByName.get(name) as GitHubAsset),
    },
  };
}

function selectCLIRelease(releases: GitHubRelease[], channel: PublicReleaseChannel): GitHubRelease | null {
  let selected: { release: GitHubRelease; order: number[] } | null = null;
  for (const release of releases) {
    const candidate = normalizeCLIRelease(release, channel);
    if (candidate && (!selected || compareOrder(candidate.order, selected.order) > 0)) selected = candidate;
  }
  return selected?.release ?? null;
}

async function fetchCLIRelease(url: string, channel: PublicReleaseChannel, source: string): Promise<Response | null> {
  try {
    const response = await fetch(url, {
      headers: { accept: "application/json", "user-agent": "reasonix-release-gateway" },
    });
    if (!response.ok) return null;
    const release = normalizeCLIRelease((await response.json()) as GitHubRelease, channel)?.release;
    return release ? new Response(JSON.stringify(release) + "\n", { status: 200, headers: gatewayHeaders(source) }) : null;
  } catch {
    return null;
  }
}

async function fetchLatestCLIReleaseFromGitHub(channel: PublicReleaseChannel): Promise<Response | null> {
  try {
    const response = await fetch(GITHUB_RELEASES_API, {
      headers: { accept: "application/vnd.github+json", "user-agent": "reasonix-release-gateway" },
    });
    if (!response.ok) return null;
    const release = selectCLIRelease((await response.json()) as GitHubRelease[], channel);
    return release
      ? new Response(JSON.stringify(release) + "\n", { status: 200, headers: gatewayHeaders("github-cli-releases") })
      : null;
  } catch {
    return null;
  }
}

function isManifestJSON(text: string): boolean {
  try {
    const data = JSON.parse(text) as { version?: unknown; platforms?: unknown };
    return typeof data.version === "string" && Boolean(data.platforms) && typeof data.platforms === "object";
  } catch {
    return false;
  }
}

async function fetchManifestText(url: string, source: string): Promise<Response | null> {
  try {
    const res = await fetch(url, {
      headers: {
        accept: "application/json",
        "user-agent": "reasonix-release-gateway",
      },
    });
    if (!res.ok) return null;
    const text = await res.text();
    if (!isManifestJSON(text)) return null;
    return new Response(text, { status: 200, headers: gatewayHeaders(source) });
  } catch {
    return null;
  }
}

async function fetchLatestDesktopManifestFromGitHub(): Promise<Response | null> {
  try {
    const list = await fetch(GITHUB_RELEASES_API, {
      headers: {
        accept: "application/vnd.github+json",
        "user-agent": "reasonix-release-gateway",
      },
    });
    if (!list.ok) return null;

    const releases = (await list.json()) as GitHubRelease[];
    const latestDesktop = releases.find(
      (r) =>
        !r.draft &&
        !r.prerelease &&
        typeof r.tag_name === "string" &&
        /^desktop-v\d+\.\d+\.\d+(?:[+-][0-9A-Za-z.-]+)?$/.test(r.tag_name),
    );
    const manifest = latestDesktop?.assets?.find((a) => a.name === "latest.json" && a.browser_download_url);
    if (!manifest?.browser_download_url) return null;
    return fetchManifestText(manifest.browser_download_url, "github-desktop-release");
  } catch {
    return null;
  }
}

export async function handleDesktopReleaseManifest(channel: ReleaseChannel): Promise<Response> {
  const r2 = await fetchManifestText(manifestPointer(channel), `r2-${channel}`);
  if (r2) return r2;

  if (channel === "preview") {
    const canary = await fetchManifestText(manifestPointer("canary"), "r2-canary-compat");
    if (canary) return canary;
  }

  if (channel === "stable") {
    const github = await fetchLatestDesktopManifestFromGitHub();
    if (github) return github;
  }

  return new Response(JSON.stringify({ error: "desktop release manifest unavailable", channel }) + "\n", {
    status: 502,
    headers: gatewayHeaders("unavailable"),
  });
}

export async function handleCLIRelease(channel: PublicReleaseChannel): Promise<Response> {
  const cacheStorage = (globalThis as unknown as {
    caches?: CacheStorage & { default?: Cache };
  }).caches;
  const cache = cacheStorage?.default;
  const cacheKey = new Request(`https://crash.reasonix.io/v1/cli/releases/${channel}/latest.json`);
  const cached = await cache?.match(cacheKey);
  if (cached) return cached;

  const pointer = await fetchCLIRelease(`${R2_BASE}/cli/${channel}/latest.json`, channel, `r2-cli-${channel}`);
  const response = pointer ?? (await fetchLatestCLIReleaseFromGitHub(channel));
  if (response) {
    await cache?.put(cacheKey, response.clone());
    return response;
  }

  return new Response(JSON.stringify({ error: "CLI release unavailable", channel }) + "\n", {
    status: 502,
    headers: gatewayHeaders("unavailable"),
  });
}

export function desktopReleaseChannel(path: string): ReleaseChannel | null {
  const match = path.match(/^\/v1\/desktop\/releases\/(stable|preview|canary)\/latest\.json$/);
  return (match?.[1] as ReleaseChannel | undefined) ?? null;
}

export function cliReleaseChannel(path: string): PublicReleaseChannel | null {
  const match = path.match(/^\/v1\/cli\/releases\/(stable|preview)\/latest\.json$/);
  return (match?.[1] as PublicReleaseChannel | undefined) ?? null;
}
