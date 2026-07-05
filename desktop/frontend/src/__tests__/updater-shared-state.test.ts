// Run: tsx src/__tests__/updater-shared-state.test.ts
//
// Verifies that useUpdater state is lifted into a React context so that
// UpdateBanner and UpdatesSection (Settings panel) share the same updater
// lifecycle. A startup check that finds an available update must be visible
// in Settings without a separate manual check (issue #5987).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;

function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const useUpdaterSource = readFileSync(resolve(here, "../lib/useUpdater.ts"), "utf8");
const appSource = readFileSync(resolve(here, "../App.tsx"), "utf8");

console.log("\nupdater shared state contract (#5987)");

// --- useUpdater.ts ---

ok(
  useUpdaterSource.includes("createContext"),
  "useUpdater creates a React context for shared state",
);

ok(
  useUpdaterSource.includes("UpdaterContext"),
  "UpdaterContext is defined for the updater state",
);

ok(
  useUpdaterSource.includes("export function UpdaterProvider"),
  "UpdaterProvider is exported so App.tsx can wrap the tree",
);

ok(
  useUpdaterSource.includes("useContext(UpdaterContext)"),
  "useUpdater reads from UpdaterContext instead of owning local state",
);

ok(
  !useUpdaterSource.includes("export function useUpdater(): Updater {\n  const [status, setStatus] = useState"),
  "useUpdater no longer owns a local useState — state is in the provider",
);

// --- App.tsx ---

ok(
  appSource.includes("import { UpdaterProvider }"),
  "App imports UpdaterProvider",
);

ok(
  appSource.includes("<UpdaterProvider>"),
  "App wraps children with <UpdaterProvider>",
);

ok(
  appSource.includes("</UpdaterProvider>"),
  "App closes </UpdaterProvider>",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
