import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { mcpListAttribution, sessionItemsToJson } from "../lib/sessionExportData";
import type { Item } from "../lib/useController";

describe("session export MCP list attribution", () => {
  it("counts shared host, disk cache, and remote tools/list sources", () => {
    const items: Item[] = [
      { kind: "tool", id: "1", name: "use_capability", args: "{}", readOnly: true, status: "done", output: JSON.stringify({ source: "shared_host", network_call: false }) },
      { kind: "tool", id: "2", name: "use_capability", args: "{}", readOnly: true, status: "done", output: JSON.stringify({ source: "disk_cache" }) },
      { kind: "tool", id: "3", name: "use_capability", args: "{}", readOnly: true, status: "done", output: JSON.stringify({ source: "remote", network_call: true }) },
      { kind: "notice", id: "4", level: "info", text: "MCP tools/list", detail: JSON.stringify({ source: "remote", network_call: true }) },
      { kind: "tool", id: "5", name: "business_api", args: "{}", readOnly: true, status: "done", output: JSON.stringify({ source: "remote", network_call: true }) },
      { kind: "notice", id: "6", level: "info", text: "deployment completed", detail: JSON.stringify({ source: "remote", network_call: true }) },
    ];
    const got = mcpListAttribution(items);
    assert.deepEqual(got, { sharedHost: 1, diskCache: 1, remote: 2, networkCalls: 2 });
    const exported = JSON.parse(sessionItemsToJson("session", items));
    assert.equal(exported.mcpList.sharedHost, 1);
    assert.equal(exported.items.length, 6);
  });
});
