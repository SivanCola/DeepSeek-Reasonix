import { promises as fs } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ToolRegistry } from "../src/tools.js";
import { registerFilesystemTools } from "../src/tools/filesystem.js";
import { ReadTracker } from "../src/tools/read-tracker.js";

describe("delete_range tool", () => {
  let root: string;
  let tools: ToolRegistry;
  let readTracker: ReadTracker;

  beforeEach(async () => {
    root = await mkdtemp(join(tmpdir(), "reasonix-delete-tools-"));
    tools = new ToolRegistry();
    registerFilesystemTools(tools, { rootDir: root });
    readTracker = new ReadTracker();
  });

  afterEach(async () => {
    await rm(root, { recursive: true, force: true });
  });

  it("delete_range refuses unread files, then deletes an anchored range after read_file", async () => {
    await fs.writeFile(join(root, "demo.txt"), "before\nSTART\nremove\nEND\nafter\n");

    const unread = await tools.dispatch(
      "delete_range",
      { path: "demo.txt", start_anchor: "START\n", end_anchor: "END\n" },
      { readTracker },
    );
    expect(unread).toMatch(/read_file first/);

    await tools.dispatch("read_file", { path: "demo.txt" }, { readTracker });
    const out = await tools.dispatch(
      "delete_range",
      { path: "demo.txt", start_anchor: "START\n", end_anchor: "END\n" },
      { readTracker },
    );

    expect(out).toMatch(/delete_range: deleted/);
    await expect(fs.readFile(join(root, "demo.txt"), "utf8")).resolves.toBe("before\nafter\n");
  });

  it("delete_range is a no-op when anchors are duplicated", async () => {
    await fs.writeFile(join(root, "demo.txt"), "A\nSTART\nx\nSTART\nEND\n");
    await tools.dispatch("read_file", { path: "demo.txt" }, { readTracker });

    const out = await tools.dispatch(
      "delete_range",
      { path: "demo.txt", start_anchor: "START", end_anchor: "END" },
      { readTracker },
    );

    expect(out).toMatch(/no-op/);
    await expect(fs.readFile(join(root, "demo.txt"), "utf8")).resolves.toContain("x");
  });
});
