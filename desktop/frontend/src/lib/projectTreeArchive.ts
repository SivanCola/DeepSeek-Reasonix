import { useCallback, useRef, useState } from "react";

export function enqueueProjectTreeArchive(previous: Promise<void>, work: () => Promise<void>): Promise<void> {
  return previous.catch(() => undefined).then(work);
}

export async function runProjectTreeArchiveJob({
  archive,
  reload,
  finishPending,
  recover,
}: {
  archive: () => Promise<void>;
  reload: () => Promise<void>;
  finishPending: () => void;
  recover: (error: unknown) => Promise<void>;
}): Promise<boolean> {
  try {
    await archive();
  } catch (error) {
    // Failed mutations must become visible to the recovery reload.
    finishPending();
    await recover(error);
    return false;
  }
  try {
    // Keep the visible pending state active until the canonical folder page
    // has landed. The caller may release its stale-response tombstone once
    // that reload has acquired a newer request generation.
    await reload();
    return true;
  } finally {
    finishPending();
  }
}

export function projectTreeTrashingTopics(previous: Set<string>, topicId: string, trashing: boolean): Set<string> {
  const id = topicId.trim();
  if (!id || previous.has(id) === trashing) return previous;
  const next = new Set(previous);
  if (trashing) next.add(id);
  else next.delete(id);
  return next;
}

export function useProjectTreeArchiveState() {
  const topicsRef = useRef<Set<string>>(new Set());
  const tombstonesRef = useRef<Set<string>>(new Set());
  const [topics, setTopics] = useState<Set<string>>(new Set());
  const begin = useCallback((topicId: string) => {
    if (topicsRef.current.has(topicId)) return false;
    topicsRef.current = projectTreeTrashingTopics(topicsRef.current, topicId, true);
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, true);
    setTopics(topicsRef.current);
    return true;
  }, []);
  const end = useCallback((topicId: string) => {
    topicsRef.current = projectTreeTrashingTopics(topicsRef.current, topicId, false);
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, false);
    setTopics(topicsRef.current);
  }, []);
  const releaseTombstone = useCallback((topicId: string) => {
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, false);
  }, []);
  const currentTombstones = useCallback((): ReadonlySet<string> => tombstonesRef.current, []);
  return {
    trashingTopics: topics,
    beginTrashingTopic: begin,
    endTrashingTopic: end,
    releaseArchiveTombstone: releaseTombstone,
    currentArchiveTombstones: currentTombstones,
  };
}
