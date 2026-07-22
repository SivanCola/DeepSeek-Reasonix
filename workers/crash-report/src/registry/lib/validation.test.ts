import { describe, expect, it } from "vitest";
import { ListQuerySchema, PublishSchema } from "./validation";

const base = { kind: "skill", name: "demo", source: "https://example.com/SKILL.md" };
const parse = (overrides: Record<string, unknown>) => PublishSchema.safeParse({ ...base, ...overrides });

describe("PublishSchema source", () => {
  it("accepts URLs, git: shorthands, and package names install_source can resolve", () => {
    // kind=mcp so the skill-only whole-repo guard never masks a shape accept.
    for (const source of [
      "https://github.com/o/r/blob/main/SKILL.md",
      "http://example.com/x/.mcp.json",
      "https://github.com/o/r/tree/main/skills/foo",
      "git:github.com/o/r",
      "@scope/pkg",
      "my-mcp-server",
    ]) {
      expect(parse({ kind: "mcp", source }).success, source).toBe(true);
    }
  });

  it("rejects free text, bare local paths, and scheme-less hosts", () => {
    for (const source of [
      "这个来源是自己制作的",
      "just some words",
      "./skills/foo",
      "/home/me/skill",
      "C:\\Users\\me\\skill",
      "github.com/o/r",
      "git:github.com/",
    ]) {
      expect(parse({ source }).success, source).toBe(false);
    }
  });

  it("keeps the kind=skill whole-repo guard (installs every skill in the repo)", () => {
    expect(parse({ kind: "skill", source: "https://github.com/o/r" }).success).toBe(false);
  });

  it("allows a whole-repo GitHub source for kind=mcp (a repo root is a valid plugin/MCP source)", () => {
    expect(parse({ kind: "mcp", source: "https://github.com/o/r" }).success).toBe(true);
  });

  it("rejects a bare package name for kind=skill (it would resolve as an MCP, not a skill)", () => {
    for (const source of ["123", "my-skill", "@scope/pkg"]) {
      expect(parse({ kind: "skill", source }).success, source).toBe(false);
      expect(parse({ kind: "mcp", source }).success, source).toBe(true);
    }
  });

  it("accepts plugin repositories and the explicit plugin install kind", () => {
    for (const source of [
      "https://github.com/o/r",
      "https://github.com/o/r/tree/main/plugins/demo",
      "git:github.com/o/r",
    ]) {
      const result = parse({ kind: "plugin", installKind: "plugin", source });
      expect(result.success, source).toBe(true);
      if (result.success) expect(result.data.installKind).toBe("plugin");
    }
  });

  it("defaults plugin submissions to the explicit plugin installer", () => {
    const result = parse({ kind: "plugin", source: "https://github.com/o/r" });
    expect(result.success).toBe(true);
    if (result.success) expect(result.data.installKind).toBe("plugin");
  });

  it("rejects non-GitHub sources for kind=plugin", () => {
    for (const source of ["123", "my-plugin", "@scope/pkg", "https://example.com/reasonix-plugin.json"]) {
      expect(parse({ kind: "plugin", installKind: "plugin", source }).success, source).toBe(false);
    }
  });

  it("rejects a conflicting installer for kind=plugin", () => {
    expect(
      parse({ kind: "plugin", installKind: "mcp", source: "https://github.com/o/r" }).success,
    ).toBe(false);
  });
});

describe("ListQuerySchema kind", () => {
  it("accepts plugin as a first-class registry filter", () => {
    expect(ListQuerySchema.parse({ kind: "plugin" }).kind).toBe("plugin");
  });
});
