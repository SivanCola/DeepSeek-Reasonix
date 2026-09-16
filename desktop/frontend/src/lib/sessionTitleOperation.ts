import type { ProjectNode } from "./types";
import type { DictKey } from "./i18n";

// Keep the RPC's string argument compatible with older topic callers while
// selecting the exact durable row whenever the catalog supplies its identity.
export function sessionTitleTarget(node: ProjectNode): string {
  if (node.session?.sessionId) return `session-id:${node.session.sessionId}`;
  return node.sessionPath?.trim() || node.topicId?.trim() || "";
}

export function sessionTitleErrorKey(error: unknown): DictKey {
  const message = error instanceof Error ? error.message : String(error);
  const code = message.match(/session_operation:([a-z_]+):/)?.[1];
  const keys: Record<string, DictKey> = {
    target_not_found: "projectTree.sessionError.targetNotFound", no_messages: "projectTree.sessionError.noMessages",
    runtime_not_open: "projectTree.sessionError.runtimeNotOpen", runtime_not_ready: "projectTree.sessionError.runtimeNotReady",
    remote_disconnected: "projectTree.sessionError.remoteDisconnected", title_conflict: "projectTree.sessionError.titleConflict",
    archived: "projectTree.sessionError.archived", operation_busy: "projectTree.sessionError.operationBusy",
  };
  // Providers, disk errors and older hosts can return paths or credentials.
  // Never render unclassified backend details in the product toast.
  return keys[code ?? ""] ?? "projectTree.sessionError.failed";
}
