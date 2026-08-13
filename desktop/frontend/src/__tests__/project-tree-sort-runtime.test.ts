import {
  createProjectTopicLoadGuard,
  resetProjectTopicPageLoads,
} from "../components/ProjectTree";

type Equal = (actual: unknown, expected: unknown, label: string) => void;

export async function runProjectTreeSortRuntimeTests(eq: Equal, projectTreeSource: string) {
  eq(
    projectTreeSource.includes("sortMode,")
      && projectTreeSource.includes("requestedSortMode ?? workbenchSortModeRef.current"),
    true,
    "topic page requests carry the selected conversation sort mode",
  );
  eq(
    projectTreeSource.includes("topicLoadGuardRef.current.invalidateAll()")
      && projectTreeSource.includes("loadProjectTopics(project, false, sortMode)"),
    true,
    "changing the conversation sort mode invalidates old pages and reloads immediately",
  );

  const loadGuard = createProjectTopicLoadGuard();
  const staleGeneration = loadGuard.begin("project-a");
  let resolveStaleLoad: (() => void) | undefined;
  let staleLoadApplied = false;
  const staleLoad = new Promise<void>((resolve) => {
    resolveStaleLoad = resolve;
  }).then(() => {
    if (loadGuard.isCurrent("project-a", staleGeneration)) staleLoadApplied = true;
  });
  loadGuard.invalidateAll();
  resolveStaleLoad?.();
  await staleLoad;
  eq(staleLoadApplied, false, "a delayed old-sort response cannot apply after sort invalidation");

  const currentGeneration = loadGuard.begin("project-a");
  eq(loadGuard.isCurrent("project-a", currentGeneration), true, "the first request for the new sort remains current");
  eq(
    resetProjectTopicPageLoads({
      "project-a": { nextCursor: "old-sort-cursor", loading: true },
      "project-b": { nextCursor: "another-old-cursor", loading: false },
    }),
    { "project-a": { loading: false }, "project-b": { loading: false } },
    "sort invalidation clears pagination cursors before the new first page loads",
  );
}
