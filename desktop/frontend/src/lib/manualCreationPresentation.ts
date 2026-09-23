import type { ManualSessionCreationView } from "../generated/desktopContract.generated";
import type { DictKey } from "./i18n";

export function manualCreationPresentation(item: ManualSessionCreationView): { key: DictKey; retryable: boolean } {
  const status = item.progress?.status;
  // A failed record can already have an explicitly requested retry in flight.
  if (status === "waiting_lock") return { key: "creation.waitingLock", retryable: false };
  if (status === "waiting_workspace") return { key: "creation.workspaceUnavailable", retryable: false };
  if (status === "retrying_storage") return { key: item.progress?.stage === "persisting_result" ? "creation.saving" : "creation.storageRetry", retryable: false };
  if (status === "stopping") return { key: "creation.stopping", retryable: false };
  if (status === "running") return { key: item.progress?.slow ? "creation.slow" : "creation.running", retryable: false };
  if (status === "queued") return { key: "creation.queued", retryable: false };
  // Older hosts persisted the typed operation code in the error string. Read
  // only that code; paths and implementation details never enter UI copy.
  const code = /session_operation:([^:]+):/.exec(item.error || "")?.[1] || item.progress?.errorCode;
  if (code === "workspace_removed") return { key: "creation.workspaceRemoved", retryable: false };
  if (code === "workspace_changed") return { key: "creation.workspaceChanged", retryable: false };
  if (code === "creation_cancelled") return { key: "creation.cancelled", retryable: false };
  if (code === "creation_owner_conflict") return { key: "creation.ownerConflict", retryable: false };
  if (status === "blocked") return { key: "creation.blocked", retryable: true };
  if (item.phase === "failed") return { key: "creation.failed", retryable: true };
  return { key: "creation.pending", retryable: false };
}
