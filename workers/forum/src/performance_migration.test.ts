import { describe, expect, it } from "vitest";
import schema from "../schema.sql?raw";
import migration from "../migrate-performance-indexes.sql?raw";

describe("forum performance indexes", () => {
  it("keeps fresh installs and the additive migration aligned", () => {
    for (const sql of [schema, migration]) {
      expect(sql).toMatch(/CREATE INDEX IF NOT EXISTS posts_author_created_at\s+ON posts \(author, created_at\)/);
      expect(sql).toMatch(/CREATE INDEX IF NOT EXISTS posts_visible_topic\s+ON posts \(topic_id, created_at\)\s+WHERE status = 'visible'/);
      expect(sql).toMatch(/CREATE INDEX IF NOT EXISTS topics_visible_latest\s+ON topics \(pinned DESC, last_post_at DESC\)\s+WHERE status <> 'hidden'/);
      expect(sql).toMatch(/CREATE INDEX IF NOT EXISTS topics_visible_top\s+ON topics \(reply_count DESC, last_post_at DESC\)\s+WHERE status <> 'hidden'/);
    }
  });

  it("keeps the migration additive and idempotent", () => {
    expect(migration).not.toMatch(/\b(?:DROP|ALTER)\b/);
    expect(migration.match(/CREATE INDEX IF NOT EXISTS/g)).toHaveLength(4);
  });
});
