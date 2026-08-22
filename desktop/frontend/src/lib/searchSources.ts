export type SearchSource = {
  title?: string;
  url?: string;
};

export type SearchSourceView = {
  title: string;
  href: string;
  hostname: string;
  displayUrl: string;
  canonicalUrl: string;
};

export type SearchSourcePresentation = {
  visible: SearchSourceView[];
  hiddenCount: number;
};

export interface HistoryServerSearch {
  id: string;
  query?: string;
  results?: { title?: string; url?: string }[];
}

export function isSafeHttpUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
}

const REDIRECT_HOSTS = new Set([
  "page.sm.cn",
  "www.google.com",
  "google.com",
  "www.bing.com",
  "bing.com",
]);
const REDIRECT_PATHS = new Set(["/url", "/url/", "/ck/a"]);
const REDIRECT_PARAMS = ["url", "u", "q", "target", "dest", "destination", "redirect", "redirect_url"] as const;
const TRACKING_PARAMS = new Set([
  "fbclid",
  "gclid",
  "dclid",
  "gbraid",
  "wbraid",
  "msclkid",
  "mc_cid",
  "mc_eid",
  "igshid",
]);

function isRedirectWrapper(parsed: URL): boolean {
  const host = parsed.hostname.toLowerCase();
  if (host === "page.sm.cn") return true;
  return REDIRECT_HOSTS.has(host) && REDIRECT_PATHS.has(parsed.pathname.toLowerCase());
}

function unwrapRedirectUrl(parsed: URL): URL | null {
  if (!isRedirectWrapper(parsed)) return parsed;
  for (const key of REDIRECT_PARAMS) {
    const candidate = parsed.searchParams.get(key);
    if (!candidate || !isSafeHttpUrl(candidate)) continue;
    try {
      return new URL(candidate);
    } catch {
      continue;
    }
  }
  return null;
}

function canonicalizeUrl(raw: string): URL | null {
  try {
    const parsed = new URL(raw);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
    const unwrapped = unwrapRedirectUrl(parsed);
    if (!unwrapped) return null;
    for (const key of [...unwrapped.searchParams.keys()]) {
      if (key.toLowerCase().startsWith("utm_") || TRACKING_PARAMS.has(key.toLowerCase())) {
        unwrapped.searchParams.delete(key);
      }
    }
    unwrapped.hash = "";
    return unwrapped;
  } catch {
    return null;
  }
}

function shortDisplayUrl(parsed: URL): string {
  const host = parsed.hostname.replace(/^www\./i, "");
  const path = `${parsed.pathname}${parsed.search}`;
  const display = `${host}${path === "/" ? "" : path}`;
  return display.length <= 92 ? display : `${display.slice(0, 91)}…`;
}

/** Build a safe, compact display projection without changing replay data. */
export function normalizeSearchSources(sources: SearchSource[] | undefined): SearchSourcePresentation {
  const visible: SearchSourceView[] = [];
  const seen = new Set<string>();
  let hiddenCount = 0;
  for (const source of sources ?? []) {
    const title = (source.title ?? "").trim();
    const parsed = canonicalizeUrl((source.url ?? "").trim());
    if (!title || !parsed) {
      hiddenCount += 1;
      continue;
    }
    const canonicalUrl = parsed.toString();
    if (seen.has(canonicalUrl)) {
      hiddenCount += 1;
      continue;
    }
    seen.add(canonicalUrl);
    visible.push({
      title,
      href: canonicalUrl,
      hostname: parsed.hostname.replace(/^www\./i, ""),
      displayUrl: shortDisplayUrl(parsed),
      canonicalUrl,
    });
  }
  return { visible, hiddenCount };
}

function sourceKey(source: SearchSource): string {
  return `${source.url ?? ""}\n${source.title ?? ""}`;
}

export function mergeSearchSources(dst: SearchSource[] | undefined, add: SearchSource[]): SearchSource[] {
  const out = dst ? dst.slice() : [];
  const seen = new Set(out.map(sourceKey));
  for (const hit of add) {
    if (!hit.title && !hit.url) continue;
    const key = sourceKey(hit);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ title: hit.title, url: hit.url });
  }
  return out;
}

export function searchSourcesFromHistory(searches: { results?: { title?: string; url?: string }[] }[] | undefined): SearchSource[] {
  const add: SearchSource[] = [];
  for (const search of searches ?? []) {
    for (const hit of search.results ?? []) {
      if (hit.title || hit.url) add.push({ title: hit.title, url: hit.url });
    }
  }
  return mergeSearchSources(undefined, add);
}

export function parseSearchSources(output: string): SearchSource[] {
  const lines = output.split("\n").map((line) => line.trim()).filter(Boolean);
  const out: SearchSource[] = [];
  for (const line of lines) {
    // Tolerate the footnote-markdown shape (`- **title**` / `<url>`) as well:
    // if a degraded plain-text dump ever reaches this parser (#8900), sources
    // still resolve into cards/footnotes instead of leaking raw markup.
    const urlMatch = /^<?(https?:\/\/[^>\s]+)>?$/i.exec(line);
    if (urlMatch) {
      const last = out[out.length - 1];
      if (last && !last.url) last.url = urlMatch[1];
      else out.push({ url: urlMatch[1] });
      continue;
    }
    const titleMatch = /^[-*]\s+\*\*(.+)\*\*$/.exec(line);
    out.push({ title: titleMatch?.[1] ?? line });
  }
  return out;
}

/** Same title + autolink list the old answer dump used, rendered after the reply. */
export function formatSearchFootnotesMarkdown(sources: SearchSource[]): string {
  const lines: string[] = [];
  for (const source of sources) {
    if (!source.title && !source.url) continue;
    lines.push(`- **${source.title ?? ""}**`);
    if (source.url && isSafeHttpUrl(source.url)) lines.push(`  <${source.url}>`);
  }
  return lines.length > 0 ? `\n${lines.join("\n")}\n` : "";
}
