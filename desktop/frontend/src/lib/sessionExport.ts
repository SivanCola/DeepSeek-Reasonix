import type { Item, LiveStream } from "./useController";

export type McpListAttribution = {
  sharedHost: number;
  diskCache: number;
  remote: number;
  networkCalls: number;
};

export function materializeLiveItems(items: Item[], live?: LiveStream): Item[] {
  if (!live) return items;
  return items.map((item) => {
    if (item.kind !== "assistant" || item.id !== live.id) return item;
    return { ...item, text: live.text, reasoning: live.reasoning, streaming: true };
  });
}

function fence(label: string, value: string): string {
  if (!value.trim()) return "";
  const fenceToken = value.includes("```") ? "````" : "```";
  return `${label}\n${fenceToken}\n${value.trim()}\n${fenceToken}`;
}

export function sessionItemsToMarkdown(title: string, items: Item[], live?: LiveStream): string {
  const lines: string[] = [`# ${title.trim() || "Reasonix session"}`, ""];
  for (const item of materializeLiveItems(items, live)) {
    switch (item.kind) {
      case "user":
        lines.push("## User", "", item.text.trim(), "");
        break;
      case "assistant":
        lines.push("## Assistant");
        if (item.reasoning.trim()) {
          lines.push("", "### Reasoning", "", item.reasoning.trim());
        }
        if (item.text.trim()) {
          lines.push("", item.text.trim());
        }
        lines.push("");
        break;
      case "tool":
        lines.push(`### Tool: ${item.name}`);
        if (item.args.trim()) lines.push("", fence("Args", item.args));
        if (item.output?.trim()) lines.push("", fence("Output", item.output));
        if (item.error?.trim()) lines.push("", fence("Error", item.error));
        lines.push("");
        break;
      case "phase":
        lines.push(`### Phase`, "", item.text.trim(), "");
        break;
      case "notice":
        lines.push(`### ${item.level === "warn" ? "Warning" : "Notice"}`, "", item.text.trim(), "");
        if (item.detail?.trim()) {
          lines.push("Details:", "", item.detail.trim(), "");
        }
        break;
      case "compaction":
        lines.push("### Context Compaction", "");
        if (item.pending) {
          lines.push("Compaction pending.");
        } else {
          lines.push(`Messages: ${item.messages}`);
          if (item.trigger) lines.push(`Trigger: ${item.trigger}`);
          if (item.summary.trim()) lines.push("", item.summary.trim());
        }
        lines.push("");
        break;
    }
  }
  return lines.join("\n").replace(/\n{3,}/g, "\n\n").trimEnd() + "\n";
}

export function mcpListAttribution(items: Item[]): McpListAttribution {
  const counts: McpListAttribution = { sharedHost: 0, diskCache: 0, remote: 0, networkCalls: 0 };
  for (const item of items) {
    if (item.kind === "notice") {
      const text = `${item.text} ${item.detail ?? ""}`.toLowerCase();
      if (text.includes("tools/list") && (text.includes("remote") || text.includes("network"))) {
        counts.remote += 1;
      }
      continue;
    }
    if (item.kind !== "tool" || !item.output) continue;
    const source = jsonStringField(item.output, "source");
    if (source === "shared_host") counts.sharedHost += 1;
    else if (source === "disk_cache") counts.diskCache += 1;
    else if (source === "remote") counts.remote += 1;
    if (jsonBooleanField(item.output, "network_call")) counts.networkCalls += 1;
  }
  return counts;
}

function jsonStringField(raw: string, key: string): string | undefined {
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const value = parsed[key];
    return typeof value === "string" ? value : undefined;
  } catch {
    return undefined;
  }
}

function jsonBooleanField(raw: string, key: string): boolean {
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    return parsed[key] === true;
  } catch {
    return false;
  }
}

export function sessionItemsToJson(title: string, items: Item[], live?: LiveStream): string {
  const materialized = materializeLiveItems(items, live);
  return JSON.stringify(
    {
      title,
      exportedAt: new Date().toISOString(),
      mcpList: mcpListAttribution(materialized),
      items: materialized,
    },
    null,
    2,
  );
}
