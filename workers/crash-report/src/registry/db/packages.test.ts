import { describe, expect, it } from "vitest";
import type { PackageRow, RegistryUser } from "../types";
import { PublishSchema } from "../lib/validation";
import { PackageRepo } from "./packages";

const now = "2026-07-22T00:00:00.000Z";
const user: RegistryUser = {
  id: 7,
  handle: "publisher",
  role: "member",
  emailVerified: true,
};

const existing: PackageRow = {
  id: 42,
  kind: "mcp",
  scope_handle: "publisher",
  name: "devkit",
  slug: "publisher/devkit",
  summary: "old",
  description: "",
  source: "https://github.com/o/r",
  install_kind: "auto",
  homepage: "",
  repo_url: "https://github.com/o/r",
  tags: "tool",
  latest_version: "2.7.0",
  status: "pending",
  verified: 0,
  publisher_id: 7,
  install_count: 0,
  star_count: 0,
  created_at: now,
  updated_at: now,
};

describe("PackageRepo.publish", () => {
  it("persists a kind change when an owned pending package is republished as a plugin", async () => {
    const updated: PackageRow = { ...existing, kind: "plugin", install_kind: "plugin", latest_version: "2.7.1" };
    const updates: { sql: string; values: unknown[] }[] = [];
    let packageReads = 0;

    const db = {
      prepare(sql: string) {
        let values: unknown[] = [];
        const statement = {
          bind(...bound: unknown[]) {
            values = bound;
            return statement;
          },
          async first<T>() {
            if (sql.startsWith("SELECT * FROM packages")) {
              packageReads += 1;
              return (packageReads === 1 ? existing : updated) as T;
            }
            return null;
          },
          async run() {
            if (sql.startsWith("UPDATE packages SET")) updates.push({ sql, values });
            return { meta: { changes: 1 } };
          },
        };
        return statement;
      },
    };

    const input = PublishSchema.parse({
      kind: "plugin",
      installKind: "plugin",
      name: "devkit",
      source: "https://github.com/o/r",
      repoUrl: "https://github.com/o/r",
      version: "2.7.1",
    });
    const result = await new PackageRepo(db as unknown as D1Database).publish(user, input, now);

    expect(result.created).toBe(false);
    expect(result.row.kind).toBe("plugin");
    expect(updates).toHaveLength(1);
    expect(updates[0].sql).toContain("SET kind = ?1");
    expect(updates[0].values[0]).toBe("plugin");
    expect(updates[0].values[4]).toBe("plugin");
    expect(updates[0].values[10]).toBe(existing.id);
  });
});
