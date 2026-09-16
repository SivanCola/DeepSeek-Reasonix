import { useCallback, useEffect, useState } from "react";
import type { ProjectNode } from "../lib/types";
import { projectTreeMigrateReadActivity, projectTreeReadActivityKey, projectTreeSeedReadActivity, topicReadRevision, type ProjectTreeReadActivity } from "../lib/projectTreeTopic";

const READ_ACTIVITY_KEY = "projectTree:readActivity";
const READ_ACTIVITY_BASELINE_KEY = "projectTree:readActivityBaselineAt";
const READ_ACTIVITY_VERSION_KEY = "projectTree:readActivityVersion";
const READ_ACTIVITY_VERSION = 2;

function loadReadActivity(): ProjectTreeReadActivity {
  try {
    const raw = localStorage.getItem(READ_ACTIVITY_KEY);
    const parsed = raw ? JSON.parse(raw) as Record<string, unknown> : {};
    const out: ProjectTreeReadActivity = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "number" && Number.isFinite(value)) out[key] = value;
    }
    const storedVersion = Number(localStorage.getItem(READ_ACTIVITY_VERSION_KEY));
    const migrated = projectTreeMigrateReadActivity(out, Number.isFinite(storedVersion) ? storedVersion : 0);
    if (migrated !== out) localStorage.setItem(READ_ACTIVITY_KEY, JSON.stringify(migrated));
    if (!Number.isFinite(storedVersion) || storedVersion < READ_ACTIVITY_VERSION) {
      localStorage.setItem(READ_ACTIVITY_VERSION_KEY, String(READ_ACTIVITY_VERSION));
    }
    return migrated;
  } catch {
    return {};
  }
}

function saveReadActivity(readActivity: ProjectTreeReadActivity) {
  try {
    localStorage.setItem(READ_ACTIVITY_KEY, JSON.stringify(readActivity));
  } catch {
    /* localStorage unavailable */
  }
}

function loadReadActivityBaselineAt(): number {
  try {
    const parsed = Number(localStorage.getItem(READ_ACTIVITY_BASELINE_KEY));
    if (Number.isFinite(parsed) && parsed > 0) return parsed;
    const now = Date.now();
    localStorage.setItem(READ_ACTIVITY_BASELINE_KEY, String(now));
    return now;
  } catch {
    return Date.now();
  }
}

export function useProjectTreeReadActivity(nodes: readonly ProjectNode[]) {
  const [readActivity, setReadActivity] = useState<ProjectTreeReadActivity>(loadReadActivity);
  const [readBaselineAt] = useState(loadReadActivityBaselineAt);
  const markNodeRead = useCallback((node: ProjectNode) => {
    const key = projectTreeReadActivityKey(node);
    const revision = topicReadRevision(node);
    if (!key || revision <= 0) return;
    setReadActivity((current) => {
      const readAt = node.session ? revision : Math.max(revision, Date.now());
      if ((current[key] ?? 0) >= readAt) return current;
      const next = { ...current, [key]: readAt };
      saveReadActivity(next);
      return next;
    });
  }, []);

  useEffect(() => {
    setReadActivity((current) => {
      const next = projectTreeSeedReadActivity(nodes, current);
      if (next !== current) saveReadActivity(next);
      return next;
    });
  }, [nodes]);

  return { readActivity, readBaselineAt, markNodeRead };
}
