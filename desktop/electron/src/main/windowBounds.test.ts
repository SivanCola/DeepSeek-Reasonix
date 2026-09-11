import assert from "node:assert/strict";
import test from "node:test";
import { persistedWindowRect, type PersistedBoundsSource, type WindowRect } from "./windowBounds.js";

const NORMAL: WindowRect = { x: 40, y: 50, width: 1240, height: 720 };
const FRAME: WindowRect = { x: -7, y: -7, width: 1722, height: 1034 };

function fakeWindow(state: { maximized?: boolean; minimized?: boolean; fullscreen?: boolean }): PersistedBoundsSource {
  return {
    isMaximized: () => state.maximized ?? false,
    isMinimized: () => state.minimized ?? false,
    isFullScreen: () => state.fullscreen ?? false,
    getBounds: () => FRAME,
    getNormalBounds: () => NORMAL,
  };
}

test("normal windows persist their live bounds", () => {
  assert.deepEqual(persistedWindowRect(fakeWindow({})), FRAME);
});

test("maximized windows persist the restore rectangle, not the maximized frame", () => {
  assert.deepEqual(persistedWindowRect(fakeWindow({ maximized: true })), NORMAL);
});

test("minimized windows persist the restore rectangle, not the iconic position", () => {
  assert.deepEqual(persistedWindowRect(fakeWindow({ minimized: true })), NORMAL);
});

test("fullscreen windows persist the restore rectangle", () => {
  assert.deepEqual(persistedWindowRect(fakeWindow({ fullscreen: true })), NORMAL);
});
