import type { Item } from "./useController";

export type McpListAttribution = {
  sharedHost: number;
  diskCache: number;
  remote: number;
  networkCalls: number;
};

export function mcpListAttribution(items: Item[]): McpListAttribution {
  const counts: McpListAttribution = { sharedHost: 0, diskCache: 0, remote: 0, networkCalls: 0 };
  for (const item of items) {
    if (item.kind === "notice") {
      const text = `${item.text} ${item.detail ?? ""}`.toLowerCase();
      if (!text.includes("tools/list")) continue;
      try {
        const detail = JSON.parse(item.detail ?? "") as Record<string, unknown>;
        if (detail.source === "remote") counts.remote += 1;
        if (detail.source === "shared_host") counts.sharedHost += 1;
        if (detail.source === "disk_cache") counts.diskCache += 1;
        if (detail.network_call === true) counts.networkCalls += 1;
        if (typeof detail.source === "string") continue;
      } catch {
        // Older sessions recorded attribution only in human-readable text.
      }
      if (text.includes("remote") || text.includes("network")) {
        counts.remote += 1;
        counts.networkCalls += 1;
      }
      continue;
    }
    if (item.kind !== "tool" || item.name !== "use_capability" || !item.output) continue;
    try {
      const output = JSON.parse(item.output) as Record<string, unknown>;
      if (output.source === "shared_host") counts.sharedHost += 1;
      else if (output.source === "disk_cache") counts.diskCache += 1;
      else if (output.source === "remote") counts.remote += 1;
      if (output.network_call === true) counts.networkCalls += 1;
    } catch {
      // Ordinary tool output is not attribution metadata.
    }
  }
  return counts;
}
