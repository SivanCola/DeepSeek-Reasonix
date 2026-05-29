import { readFileSync } from "node:fs";
import { relative, resolve } from "node:path";
import { type EditBlock, toWholeFileEditBlock } from "../../code/edit-blocks.js";
import { decodeFileBuffer } from "../../code/file-encoding.js";
import type { EditMode } from "../../config.js";
import { looksLikeAbsoluteSystemPath, pathIsUnder } from "../../tools/filesystem.js";
import { computeDeleteRangePatchFromText } from "../../tools/fs/edit.js";
import type { ReadTracker } from "../../tools/read-tracker.js";

export type ReviewGatedEditTool = "edit_file" | "write_file" | "multi_edit" | "delete_range";

export function isReviewGatedEditTool(name: string): name is ReviewGatedEditTool {
  return (
    name === "edit_file" ||
    name === "write_file" ||
    name === "multi_edit" ||
    name === "delete_range"
  );
}

function resolveEditPathInfo(
  rawPath: unknown,
  rootForEdit: string,
): { rel: string; abs: string } | null {
  if (typeof rawPath !== "string" || rawPath.length === 0) return null;

  const absRoot = resolve(rootForEdit);
  if (looksLikeAbsoluteSystemPath(rawPath)) {
    const abs = resolve(rawPath);
    if (!pathIsUnder(abs, absRoot)) return null;
    const rel = relative(absRoot, abs);
    return rel ? { rel, abs } : null;
  }

  let stripped = rawPath;
  while (stripped.startsWith("/") || stripped.startsWith("\\")) {
    stripped = stripped.slice(1);
  }
  if (!stripped) return null;
  const abs = resolve(absRoot, stripped);
  if (!pathIsUnder(abs, absRoot)) return null;
  return { rel: stripped, abs };
}

function resolveEditRelPath(rawPath: unknown, rootForEdit: string): string | null {
  return resolveEditPathInfo(rawPath, rootForEdit)?.rel ?? null;
}

export function buildEditToolBlocks(
  name: string,
  args: Record<string, unknown>,
  rootForEdit: string,
): EditBlock[] | null {
  if (!isReviewGatedEditTool(name)) return null;

  if (name === "multi_edit") {
    const edits = args.edits;
    if (!Array.isArray(edits) || edits.length === 0) return null;
    const blocks: EditBlock[] = [];
    for (const item of edits) {
      if (!item || typeof item !== "object") return null;
      const edit = item as Record<string, unknown>;
      const relPath = resolveEditRelPath(edit.path, rootForEdit);
      if (!relPath || typeof edit.search !== "string" || typeof edit.replace !== "string") {
        return null;
      }
      if (edit.search.length === 0) return null;
      blocks.push({
        path: relPath,
        search: edit.search,
        replace: edit.replace,
        offset: 0,
      });
    }
    return blocks;
  }

  const relPath = resolveEditRelPath(args.path, rootForEdit);
  if (!relPath) return null;

  if (name === "edit_file") {
    const search = typeof args.search === "string" ? args.search : "";
    const replace = typeof args.replace === "string" ? args.replace : "";
    if (!search) return null;
    return [{ path: relPath, search, replace, offset: 0 }];
  }

  // write_file only — delete_range is handled
  // by specialized builders in buildEditToolBlocksForReview().
  if (name !== "write_file") return null;

  const content = typeof args.content === "string" ? args.content : "";
  return [toWholeFileEditBlock(relPath, content, rootForEdit)];
}

export async function buildEditToolBlocksForReview(
  name: string,
  args: Record<string, unknown>,
  rootForEdit: string,
  readTracker?: ReadTracker,
): Promise<EditBlock[] | null> {
  const direct = buildEditToolBlocks(name, args, rootForEdit);
  if (direct) return direct;
  try {
    if (name === "delete_range") return buildDeleteRangeBlock(args, rootForEdit, readTracker);
  } catch {
    return null;
  }
  return null;
}

function buildDeleteRangeBlock(
  args: Record<string, unknown>,
  rootForEdit: string,
  readTracker?: ReadTracker,
): EditBlock[] | null {
  const info = resolveEditPathInfo(args.path, rootForEdit);
  if (!info) return null;
  if (readTracker && !readTracker.hasRead(info.abs)) return null;
  if (typeof args.start_anchor !== "string" || typeof args.end_anchor !== "string") return null;
  const text = decodeFileBuffer(readFileSync(info.abs)).text;
  const le = text.includes("\r\n") ? "\r\n" : "\n";
  const patch = computeDeleteRangePatchFromText(text, {
    start_anchor: args.start_anchor.replace(/\r?\n/g, le),
    end_anchor: args.end_anchor.replace(/\r?\n/g, le),
    inclusive: args.inclusive !== false,
  });
  if (patch.noopReason || patch.search.length === 0) return null;
  return [{ path: info.rel, search: patch.search, replace: patch.replace, offset: 0 }];
}

export function shouldApplyEditToolImmediately(
  editMode: EditMode,
  turnEditPolicy: "ask" | "apply-all",
): boolean {
  return editMode === "auto" || editMode === "yolo" || turnEditPolicy === "apply-all";
}

export function formatQueuedReviewToolResult(blockCount: number): string {
  const noun = blockCount === 1 ? "edit" : "edits";
  return `Queued ${blockCount} ${noun} for review. No files were changed. Ask the user to run /apply to accept them or /discard to reject them.`;
}
