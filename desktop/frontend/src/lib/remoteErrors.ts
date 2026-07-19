import type { RemoteConnectionStatus } from "./types";

export type RemoteConnectionErrorKind =
  | "connection_failed"
  | "auth_failed"
  | "host_key_rejected"
  | "host_key_mismatch";

export type RemoteConnectionErrorSummaryKey = `remote.error.summary.${RemoteConnectionErrorKind}`;

export function remoteConnectionErrorKind(status?: RemoteConnectionStatus): RemoteConnectionErrorKind {
  return status?.errorDetails?.code ?? "connection_failed";
}

export function remoteConnectionErrorSummaryKey(status?: RemoteConnectionStatus): RemoteConnectionErrorSummaryKey {
  return `remote.error.summary.${remoteConnectionErrorKind(status)}`;
}

export function isRemoteHostKeyMismatch(status?: RemoteConnectionStatus): boolean {
  return remoteConnectionErrorKind(status) === "host_key_mismatch";
}
