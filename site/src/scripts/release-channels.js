const STABLE_TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const PREVIEW_TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-preview\.(0|[1-9]\d*)$/;

// Keep in lockstep with workers/crash-report CLI asset gate.
export const CLI_RELEASE_ASSETS = [
  "reasonix-darwin-amd64.tar.gz",
  "reasonix-darwin-arm64.tar.gz",
  "reasonix-linux-amd64.tar.gz",
  "reasonix-linux-arm64.tar.gz",
  "reasonix-windows-amd64.zip",
  "reasonix-windows-arm64.zip",
  "SHA256SUMS",
];

export const publicReleaseChannels = new Set(["stable", "preview"]);

export function normalizePublicReleaseChannel(value) {
  return value === "preview" ? "preview" : "stable";
}

export function cliUpgradeCommand(value) {
  return `reasonix upgrade ${normalizePublicReleaseChannel(value)}`;
}

function parsePublicTag(tag) {
  const value = String(tag || "").trim();
  let match = value.match(STABLE_TAG);
  if (match) {
    return { tag: value, channel: "stable", order: match.slice(1).map(Number) };
  }
  match = value.match(PREVIEW_TAG);
  if (match) {
    return { tag: value, channel: "preview", order: match.slice(1).map(Number) };
  }
  return null;
}

function compareOrder(left, right) {
  for (let i = 0; i < Math.max(left.length, right.length); i += 1) {
    const delta = (left[i] || 0) - (right[i] || 0);
    if (delta !== 0) return delta;
  }
  return 0;
}

function safeHTTPSURL(value) {
  try {
    const url = new URL(String(value || ""));
    return url.protocol === "https:" ? url : null;
  } catch {
    return null;
  }
}

// Returns the 7 required CLI asset URLs, or null when any required asset is
// missing. Never synthesizes download URLs — incomplete releases must be
// rejected so the site never advertises 404 links.
export function releaseAssetMap(release) {
  const found = {};
  for (const asset of Array.isArray(release?.assets) ? release.assets : []) {
    const name = String(asset?.name || "");
    const url = safeHTTPSURL(asset?.browser_download_url);
    if (name && url) found[name] = url.href;
  }
  const assets = {};
  for (const name of CLI_RELEASE_ASSETS) {
    if (!found[name]) return null;
    assets[name] = found[name];
  }
  return assets;
}

export function selectCLIRelease(releases, requestedChannel) {
  const channel = normalizePublicReleaseChannel(requestedChannel);
  let selected = null;
  let selectedTag = null;
  for (const release of Array.isArray(releases) ? releases : []) {
    const parsed = parsePublicTag(release?.tag_name);
    if (!parsed || parsed.channel !== channel) continue;
    if (Boolean(release?.prerelease) !== (channel === "preview")) continue;
    if (!releaseAssetMap(release)) continue;
    if (!selectedTag || compareOrder(parsed.order, selectedTag.order) > 0) {
      selected = release;
      selectedTag = parsed;
    }
  }
  return selected;
}

export function cliReleaseModel(releases, requestedChannel) {
  const channel = normalizePublicReleaseChannel(requestedChannel);
  const release = selectCLIRelease(releases, channel);
  if (!release) return null;
  const parsed = parsePublicTag(release.tag_name);
  if (!parsed) return null;
  const assets = releaseAssetMap(release);
  if (!assets) return null;
  return {
    channel,
    version: parsed.tag,
    displayVersion: parsed.tag.slice(1),
    assets,
    releaseURL: String(release.html_url || `https://github.com/esengine/DeepSeek-Reasonix/releases/tag/${parsed.tag}`),
  };
}

function manifestAsset(manifest, group, key) {
  const url = safeHTTPSURL(manifest?.[group]?.[key]?.url);
  return url?.href || "";
}

export function desktopReleaseModel(manifest, requestedChannel) {
  const channel = normalizePublicReleaseChannel(requestedChannel);
  const parsed = parsePublicTag(manifest?.version);
  if (!parsed || parsed.channel !== channel) return null;

  const primary = manifestAsset(manifest, "platforms", "darwin-arm64");
  const baseURL = safeHTTPSURL(primary);
  if (!baseURL) return null;
  baseURL.pathname = baseURL.pathname.slice(0, baseURL.pathname.lastIndexOf("/") + 1);
  baseURL.search = "";
  baseURL.hash = "";
  const derived = (name) => new URL(name, baseURL).href;

  const assets = {
    "Reasonix-darwin-universal.dmg": derived("Reasonix-darwin-universal.dmg"),
    "Reasonix-darwin-arm64.zip": manifestAsset(manifest, "platforms", "darwin-arm64"),
    "Reasonix-darwin-amd64.zip": manifestAsset(manifest, "platforms", "darwin-amd64"),
    "Reasonix-windows-amd64-installer.exe": manifestAsset(manifest, "platforms", "windows-amd64"),
    "Reasonix-windows-arm64-installer.exe": manifestAsset(manifest, "platforms", "windows-arm64"),
    "Reasonix-windows-amd64.zip": derived("Reasonix-windows-amd64.zip"),
    "Reasonix-linux-amd64.deb": manifestAsset(manifest, "native_packages", "linux-amd64"),
    "Reasonix-linux-amd64.tar.gz": manifestAsset(manifest, "platforms", "linux-amd64"),
  };
  if (Object.values(assets).some((url) => !url)) return null;
  return {
    channel,
    version: parsed.tag,
    displayVersion: parsed.tag.slice(1),
    assets,
  };
}

export async function fetchFirstJSON(urls, fetchImpl = fetch) {
  const failures = [];
  for (const url of urls) {
    try {
      const response = await fetchImpl(url, { cache: "no-cache" });
      if (!response.ok) throw new Error(`${response.status} ${response.statusText}`.trim());
      return await response.json();
    } catch (error) {
      failures.push(`${url}: ${String(error?.message || error)}`);
    }
  }
  throw new Error(`release data unavailable (${failures.join("; ")})`);
}
