import { asArray } from "./array";
import { isRuntimeSessionNode, isTopicNode, type WorkbenchOrganizeMode, type WorkbenchSortMode } from "./projectTreeTopic";
import { topicActivityTime } from "./session";
import type { ProjectNode } from "./types";

export type PinnedTreeSections = {
  pinned: ProjectNode[];
  projects: ProjectNode[];
};

function topicSortValue(node: ProjectNode, sortMode: WorkbenchSortMode): number {
  if (sortMode === "created") return node.createdAt || node.lastActivityAt || 0;
  return topicActivityTime(node);
}

function isSortableConversation(node: ProjectNode): boolean {
  return isTopicNode(node) || isRuntimeSessionNode(node);
}

function projectSortValue(node: ProjectNode, sortMode: WorkbenchSortMode): number {
  return asArray(node.children).reduce((max, child) => {
    if (!isSortableConversation(child)) return max;
    return Math.max(max, topicSortValue(child, sortMode));
  }, 0);
}

function manualTopicOrder(a: ProjectNode, b: ProjectNode): number {
  const aOrder = typeof a.sortOrder === "number" && a.sortOrder >= 0 ? a.sortOrder : Number.MAX_SAFE_INTEGER;
  const bOrder = typeof b.sortOrder === "number" && b.sortOrder >= 0 ? b.sortOrder : Number.MAX_SAFE_INTEGER;
  return aOrder === bOrder ? 0 : aOrder - bOrder;
}

function sortWorkbenchChildren(children: ProjectNode[], sortMode: WorkbenchSortMode): ProjectNode[] {
  return [...children].sort((a, b) => {
    // Runtime-only rows are the same logical conversations as catalog topics
    // while a directory index catches up. Treat both shapes as sortable rows;
    // returning 0 for a session-vs-topic pair makes the final order depend on
    // whichever async snapshot happened to arrive first.
    const aConversation = isSortableConversation(a);
    const bConversation = isSortableConversation(b);
    if (!aConversation || !bConversation) return aConversation === bConversation ? 0 : aConversation ? -1 : 1;
    if (Boolean(a.pinned) !== Boolean(b.pinned)) return a.pinned ? -1 : 1;
    const manualOrder = manualTopicOrder(a, b);
    if (manualOrder !== 0) return manualOrder;
    const activityOrder = topicSortValue(b, sortMode) - topicSortValue(a, sortMode);
    if (activityOrder !== 0) return activityOrder;
    const aKey = a.topicId || a.key;
    const bKey = b.topicId || b.key;
    return aKey < bKey ? -1 : aKey > bKey ? 1 : 0;
  });
}

export function arrangeWorkbenchTree(
  nodes: ProjectNode[],
  organizeMode: WorkbenchOrganizeMode,
  sortMode: WorkbenchSortMode,
): ProjectNode[] {
  const arranged = nodes.map((node) => {
    if (node.kind !== "project" && node.kind !== "global_folder") return node;
    return { ...node, children: sortWorkbenchChildren(asArray(node.children), sortMode) };
  });
  if (organizeMode === "project") return arranged;
  const mode = organizeMode === "recent" ? "updated" : sortMode;
  return [...arranged].sort((a, b) => {
    if (Boolean(a.pinned) !== Boolean(b.pinned)) return a.pinned ? -1 : 1;
    return projectSortValue(b, mode) - projectSortValue(a, mode);
  });
}

export function arrangeClassicProjectTree(nodes: ProjectNode[], sortMode: WorkbenchSortMode): ProjectNode[] {
  return arrangeWorkbenchTree(nodes, "project", sortMode);
}

export const CLASSIC_TOPIC_PREVIEW_LIMIT = 5;

export function classicTopicWindow(children: ProjectNode[], showAll: boolean): { visible: ProjectNode[]; hiddenCount: number } {
  if (showAll || children.length <= CLASSIC_TOPIC_PREVIEW_LIMIT) return { visible: children, hiddenCount: 0 };
  return {
    visible: children.slice(0, CLASSIC_TOPIC_PREVIEW_LIMIT),
    hiddenCount: children.length - CLASSIC_TOPIC_PREVIEW_LIMIT,
  };
}

function projectTreeTopicIdentity(node: ProjectNode): string | null {
  if ((!isTopicNode(node) && !isRuntimeSessionNode(node)) || !node.topicId) return null;
  const global = node.kind === "global_topic" || node.kind === "global_session";
  return `${global ? "global" : "project"}\u001f${global ? "" : node.root ?? ""}\u001f${node.topicId}`;
}

export function splitPinnedProjectTree(
  nodes: ProjectNode[],
  sortMode: WorkbenchSortMode,
  includePinnedProjects = true,
): PinnedTreeSections {
  const pinnedTopics: ProjectNode[] = [];
  const pinnedProjects: ProjectNode[] = [];
  const projects: ProjectNode[] = [];
  // A pinned topic shell can coexist briefly with a runtime session projection.
  // Collect identities first so its source folder cannot paint it twice.
  const pinnedTopicIdentities = new Set<string>();
  for (const node of nodes) {
    const identity = projectTreeTopicIdentity(node);
    if (identity && node.pinned) pinnedTopicIdentities.add(identity);
    if (node.kind !== "project" && node.kind !== "global_folder") continue;
    for (const child of asArray(node.children)) {
      const childIdentity = projectTreeTopicIdentity(child);
      if (childIdentity && child.pinned) pinnedTopicIdentities.add(childIdentity);
    }
  }

  for (const node of nodes) {
    if (!node) continue;
    const isFolder = node.kind === "project" || node.kind === "global_folder";
    if (!isFolder) {
      const identity = projectTreeTopicIdentity(node);
      if (node.pinned) pinnedTopics.push(node);
      else if (!identity || !pinnedTopicIdentities.has(identity)) projects.push(node);
      continue;
    }

    if (includePinnedProjects && node.pinned && node.kind === "project") {
      pinnedProjects.push(node);
      continue;
    }

    const nextChildren: ProjectNode[] = [];
    for (const child of asArray(node.children)) {
      const identity = projectTreeTopicIdentity(child);
      if (isTopicNode(child) && child.pinned) {
        pinnedTopics.push(child);
        continue;
      }
      if (identity && pinnedTopicIdentities.has(identity)) continue;
      nextChildren.push(child);
    }
    projects.push({ ...node, children: nextChildren });
  }

  pinnedTopics.sort((a, b) => topicSortValue(b, sortMode) - topicSortValue(a, sortMode));
  pinnedProjects.sort((a, b) => projectSortValue(b, sortMode) - projectSortValue(a, sortMode));
  return { pinned: [...pinnedTopics, ...pinnedProjects], projects };
}
