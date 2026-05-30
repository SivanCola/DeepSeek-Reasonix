import { describe, expect, it } from "vitest";
import {
  type CopyHistoryMode,
  parseCopyHistoryArgs,
  selectCopyHistory,
} from "../src/cli/ui/copy-history.js";
import type { Card } from "../src/cli/ui/state/cards.js";

const ts = 1;

function user(id: string, text: string): Card {
  return { kind: "user", id, ts, text };
}

function assistant(id: string, text: string): Card {
  return { kind: "streaming", id, ts, text, done: true };
}

function tool(id: string, name: string, output: string): Card {
  return { kind: "tool", id, ts, name, args: { ok: true }, output, done: true, elapsedMs: 12 };
}

describe("copy history selection", () => {
  it("defaults to the latest assistant response", () => {
    const selected = selectCopyHistory([
      user("u1", "hello"),
      assistant("a1", "first"),
      assistant("a2", "second"),
    ]);

    expect(selected).toEqual({ label: "last assistant response", text: "second" });
  });

  it("copies the whole conversation when requested", () => {
    const selected = selectCopyHistory(
      [user("u1", "hello"), assistant("a1", "hi"), tool("t1", "run_command", "done")],
      { kind: "all" },
    );

    expect(selected?.text).toContain("User:\nhello");
    expect(selected?.text).toContain("Assistant:\nhi");
    expect(selected?.text).toContain("Tool run_command:");
    expect(selected?.text).toContain("Output:\ndone");
  });

  it("copies the last N serializable entries", () => {
    const selected = selectCopyHistory(
      [user("u1", "one"), assistant("a1", "two"), user("u2", "three")],
      { kind: "last", count: 2 },
    );

    expect(selected?.label).toBe("last 2 item(s)");
    expect(selected?.text).not.toContain("one");
    expect(selected?.text).toContain("Assistant:\ntwo");
    expect(selected?.text).toContain("User:\nthree");
  });

  it("returns null when there is no matching content", () => {
    expect(selectCopyHistory([], { kind: "latest-assistant" })).toBeNull();
  });
});

describe("parseCopyHistoryArgs", () => {
  it.each([
    [[], { kind: "latest-assistant" }],
    [["all"], { kind: "all" }],
    [["assistant"], { kind: "latest-assistant" }],
    [["3"], { kind: "last", count: 3 }],
  ] as Array<[string[], CopyHistoryMode]>)("parses %j", (args, expected) => {
    expect(parseCopyHistoryArgs(args)).toEqual(expected);
  });

  it("rejects unsupported forms", () => {
    expect(parseCopyHistoryArgs(["0"])).toEqual({ error: "usage" });
    expect(parseCopyHistoryArgs(["all", "extra"])).toEqual({ error: "usage" });
  });
});
