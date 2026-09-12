import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { shouldShowStartupDiagnostic } from "./startupPresentation.js";

test("a healthy first boot does not flash the diagnostic wait page", () => {
  assert.equal(shouldShowStartupDiagnostic("boot", false, false), false);
  assert.equal(shouldShowStartupDiagnostic("boot", false, true), false);
  assert.equal(shouldShowStartupDiagnostic("boot", true, true), false);
});

test("a second launch while starting still shows the wait or recovery page", () => {
  assert.equal(shouldShowStartupDiagnostic("second-instance", false, false), true);
  assert.equal(shouldShowStartupDiagnostic("second-instance", false, true), true);
  assert.equal(shouldShowStartupDiagnostic("second-instance", true, true), false);
});

test("dock activate during first boot stays quiet until a window already exists", () => {
  assert.equal(shouldShowStartupDiagnostic("activate", false, false), false);
  assert.equal(shouldShowStartupDiagnostic("activate", false, true), true);
  assert.equal(shouldShowStartupDiagnostic("activate", true, true), false);
});

test("whenReady does not create or show a provisional diagnostic window", () => {
  const source = readFileSync(fileURLToPath(new URL("./index.ts", import.meta.url)), "utf8");
  const end = source.indexOf("return service.start()");
  const start = source.lastIndexOf("void app.whenReady()", end);
  assert.ok(start >= 0 && end > start, "index.ts must still start the service from whenReady");
  const boot = source.slice(start, end);
  assert.equal(boot.includes("showFailure"), false, "first boot must not show the diagnostic wait page");
  assert.equal(boot.includes("mainWindow.create("), false, "first boot must not create a window just to flash a wait page");
});
