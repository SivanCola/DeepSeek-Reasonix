import type { FileResourceRef } from "./presentedFileNavigation";
import { pathExtension } from "./filePaths";

const MEDIA = new Set(["html", "htm", "pdf", "png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "svg", "mp3", "wav", "ogg", "m4a", "aac", "mp4", "webm", "mov", "m4v", "ogv"]);
// Formats whose bytes are not text. SVG is deliberately absent: it is an image
// to the previewer and a document to the editor, and both views are legitimate.
const BINARY = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "pdf", "mp3", "wav", "ogg", "m4a", "aac", "flac", "mp4", "webm", "mov", "m4v", "ogv", "zip", "tar", "gz", "7z", "rar", "doc", "docx", "xls", "xlsx", "ppt", "pptx"]);

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

/** Maps the host's verified action list onto the menu's capability shape. */
export function capabilityActions(actions: readonly string[]): FileResourceCapabilities {
  const has = (action: string) => actions.includes(action);
  return {
    preview: has("preview"),
    source: has("source"),
    browser: has("browser"),
    revealTree: has("reveal-tree"),
    copyPath: has("copy-path"),
    openNative: has("open-native"),
    revealNative: has("reveal-native"),
    saveCopy: has("save-copy"),
  };
}

/** The host still revalidates every action; this snapshot only controls honest UI affordances. */
export function fileResourceCapabilities(ref: FileResourceRef, verified?: readonly string[]): FileResourceCapabilities {
  // A verified reference knows its own file type, so the extension is not
  // consulted and SVG keeps its source view.
  if (verified) return capabilityActions(verified);
  const remote = ref.hostId !== "local";
  const ext = pathExtension(ref.path);
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
