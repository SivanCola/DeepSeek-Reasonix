import assert from "node:assert/strict";
import type { WorkspaceSessionSummary } from "../generated/desktopContract.generated";
import { workspaceSessionsForDisplay } from "./WorkspaceSessionBrowser";

const row = (sessionId: string, options: Partial<WorkspaceSessionSummary> = {}): WorkspaceSessionSummary => ({
  ref: { hostId: "local", sessionId }, workspaceId: "global", title: sessionId, preview: "", turns: 1,
  createdAt: 0, updatedAt: 0, blank: false, archived: false, running: false,
  metadataStatus: "ready", health: "healthy", ...options,
});

const rows = [row("one"), row("two"), row("three"), row("four"), row("five"), row("six"), row("blank", { blank: true, turns: 0, running: true }), row("old", { archived: true })];
assert.deepEqual(workspaceSessionsForDisplay(rows, false, false).map((item) => item.ref.sessionId), ["one", "two", "three", "four", "five", "blank"]);
assert.deepEqual(workspaceSessionsForDisplay(rows, true, false).map((item) => item.ref.sessionId), ["one", "two", "three", "four", "five", "six", "blank"]);
assert.deepEqual(workspaceSessionsForDisplay(rows, false, true).map((item) => item.ref.sessionId), ["old"]);
