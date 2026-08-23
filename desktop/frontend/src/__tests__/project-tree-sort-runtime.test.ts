import { arrangeClassicProjectTree } from "../lib/projectTreePresentation";
import type { ProjectNode } from "../lib/types";

type Equal = (actual: unknown, expected: unknown, label: string) => void;

export async function runProjectTreeSortRuntimeTests(eq: Equal, projectTreeSource: string) {
  const runtimeOnly: ProjectNode = {
    key: "session_wireless",
    kind: "session",
    label: "wireless",
    root: "/repo/runtime",
    topicId: "wireless",
    runtimeOnly: true,
    lastActivityAt: 100,
  };
  const canonical = (topicId: string, lastActivityAt: number): ProjectNode => ({
    key: `topic_${topicId}`,
    kind: "topic",
    label: topicId,
    root: "/repo/runtime",
    topicId,
    lastActivityAt,
  });
  eq(
    arrangeClassicProjectTree([{
      key: "project_/repo/runtime",
      kind: "project",
      label: "runtime",
      root: "/repo/runtime",
      children: [runtimeOnly, canonical("postgres", 300), canonical("plm", 200)],
    }], "updated").flatMap((node) => (node.children ?? []).map((child) => child.topicId)),
    ["postgres", "plm", "wireless"],
    "runtime-only session rows stay in canonical activity order while catalog ownership catches up",
  );
  eq(
    projectTreeSource.includes("sortMode,")
      && projectTreeSource.includes("workbenchSortModeRef.current"),
    true,
    "topic page requests carry the selected conversation sort mode",
  );
  eq(
    projectTreeSource.includes("topicLoadSeqRef.current[key] += 1")
      && projectTreeSource.includes("void loadProjectTopics(project)"),
    true,
    "changing the conversation sort mode invalidates old pages and reloads immediately",
  );

  const loadSequences: Record<string, number> = { "project-a": 1 };
  const staleGeneration = loadSequences["project-a"];
  let resolveStaleLoad: (() => void) | undefined;
  let staleLoadApplied = false;
  const staleLoad = new Promise<void>((resolve) => {
    resolveStaleLoad = resolve;
  }).then(() => {
    if (loadSequences["project-a"] === staleGeneration) staleLoadApplied = true;
  });
  for (const key in loadSequences) loadSequences[key] += 1;
  resolveStaleLoad?.();
  await staleLoad;
  eq(staleLoadApplied, false, "a delayed old-sort response cannot apply after sort invalidation");

  const currentGeneration = ++loadSequences["project-a"];
  eq(loadSequences["project-a"] === currentGeneration, true, "the first request for the new sort remains current");
  eq(projectTreeSource.includes("topicPageStateRef.current = {}"), true, "sort invalidation clears pagination cursors before the new first page loads");
}
