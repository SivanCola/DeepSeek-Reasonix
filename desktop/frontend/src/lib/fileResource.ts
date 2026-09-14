import { app } from "./bridge";

/** Where a file reference came from: an agent presentation or the workspace itself. */
export type FileResourceSource = "presented" | "workspace";

type ResourceBase = { hostId: string; tabId: string; path: string };

/** What a caller knows about a file: host, session, path, origin and tool call. */
export type FileResourceRef =
  | (ResourceBase & { source: "presented"; toolCallId: string })
  | (ResourceBase & { source: "workspace"; toolCallId?: string });

/**
 * The credentials a single read must present. Captured from the command that
 * asks for the read and never inherited by a later entry point: a workspace
 * reference carries no presented tool scope even when the same path was
 * presented earlier, so a path alone cannot re-grant an earlier presentation.
 */
export type FileAccessContext = Readonly<{
  source: FileResourceSource;
  /** Session tab whose scope authorizes the read. */
  tabId: string;
  /** Presented tool call; absent for workspace reads. */
  toolCallId?: string;
}>;

/** A file the backend entry point confirmed, with the context that reads it. */
export type ResolvedFileResource = Readonly<{
  hostId: string;
  /** Path the read entry points accept. */
  path: string;
  /** Path as the caller supplied it, for display and tree reveal. */
  requestedPath: string;
  access: FileAccessContext;
}>;

export function fileAccessContext(ref: FileResourceRef): FileAccessContext {
  return ref.source === "presented"
    ? { source: "presented", tabId: ref.tabId, toolCallId: ref.toolCallId }
    : { source: "workspace", tabId: ref.tabId };
}

export const sameAccessContext = (left: FileAccessContext, right: FileAccessContext): boolean =>
  left.source === right.source && left.tabId === right.tabId && left.toolCallId === right.toolCallId;

/**
 * Navigation resolution: only a remote workspace reference needs the backend,
 * because its relative path is meaningful on the host rather than here. Every
 * other reference is used as supplied, so an open command commits before React
 * paints and the dock never renders an empty panel first.
 */
export function resolveFileResource(ref: FileResourceRef): ResolvedFileResource | Promise<ResolvedFileResource> {
  const access = fileAccessContext(ref);
  if (ref.hostId !== "local" && ref.source === "workspace") {
    return app
      .ResolveRemoteWorkspacePathForTab(ref.tabId, ref.hostId, ref.toolCallId ?? "", ref.path)
      .then((path) => ({ hostId: ref.hostId, path, requestedPath: ref.path, access }));
  }
  return { hostId: ref.hostId, path: ref.path, requestedPath: ref.path, access };
}

/** Absolute path for copy-to-clipboard and save-a-copy, where display needs one. */
export function resolveFileResourcePath(ref: FileResourceRef): Promise<string> {
  if (ref.hostId !== "local") {
    return ref.source === "presented"
      ? app.ResolveRemotePresentedPathForTab(ref.tabId, ref.hostId, ref.toolCallId, ref.path)
      : app.ResolveRemoteWorkspacePathForTab(ref.tabId, ref.hostId, ref.toolCallId ?? "", ref.path);
  }
  return ref.source === "presented"
    ? app.ResolvePresentedPathForTab(ref.tabId, ref.toolCallId, ref.path)
    : app.ResolveWorkspacePathForTab(ref.tabId, ref.path);
}

const extension = (path: string) => path.replaceAll("\\", "/").split("/").pop()?.split(".").pop()?.toLowerCase() ?? "";
const MEDIA = new Set(["html", "htm", "pdf", "png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "svg", "mp3", "wav", "ogg", "m4a", "aac", "mp4", "webm", "mov", "m4v", "ogv"]);
const BINARY = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "svg", "pdf", "mp3", "wav", "ogg", "m4a", "aac", "flac", "mp4", "webm", "mov", "m4v", "ogv", "zip", "tar", "gz", "7z", "rar", "doc", "docx", "xls", "xlsx", "ppt", "pptx"]);

export interface FileResourceCapabilities {
  preview: boolean;
  source: boolean;
  browser: boolean;
  revealTree: boolean;
  copyPath: boolean;
  openNative: boolean;
  revealNative: boolean;
  saveCopy: boolean;
}

/** Identity-only view of a file: capabilities depend on the host and the name. */
export type FileResourceIdentity = Readonly<{ hostId: string; path: string }>;

export const fileResourceIdentity = (resource: FileResourceIdentity): FileResourceIdentity =>
  ({ hostId: resource.hostId, path: resource.path });

/** The host still revalidates every action; this snapshot only controls honest UI affordances. */
export function fileResourceCapabilities(resource: FileResourceIdentity): FileResourceCapabilities {
  const remote = resource.hostId !== "local";
  const ext = extension(resource.path);
  return {
    preview: true,
    source: !BINARY.has(ext),
    browser: !remote && MEDIA.has(ext),
    revealTree: true,
    copyPath: true,
    openNative: !remote,
    revealNative: !remote,
    saveCopy: true,
  };
}
