// Run: tsx src/__tests__/navigation-surface-transition.test.ts

import { readFileSync } from "node:fs";
import { settleNavigationSurfaceIntent } from "../lib/navigationSurfaceTransition";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

console.log("\nnavigation surface transition");

let active: number | null = 1;
active = 2; // B supersedes A before A completes.
active = 3; // C supersedes queued B.
active = settleNavigationSurfaceIntent(active, 1);
ok(active === 3, "A completion cannot release C's mask");
active = settleNavigationSurfaceIntent(active, 2);
ok(active === 3, "coalesced B completion cannot release C's mask");
active = settleNavigationSurfaceIntent(active, 3);
ok(active === null, "the latest completion releases its own mask");

const appSource = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
ok(appSource.includes("flushSync(() => setNavigationSurfaceIntent(intent))"), "navigation masking commits synchronously before the Wails await");
ok(appSource.includes("items={runtimeTransitioning ? [] : displayItems}"), "App removes source transcript rows during navigation");
ok(appSource.includes("live={runtimeTransitioning ? undefined : state.live}"), "App removes source live output during navigation");
ok(appSource.includes("hidden={composerSurfaceHidden || undefined}"), "App keeps the composer mounted but hidden during navigation");
ok(appSource.includes("inert={composerSurfaceHidden ? true : undefined}"), "the hidden composer is inert during navigation");
ok(appSource.includes("!runtimeTransitioning && showTodos"), "source-session Todo content is isolated");
ok(appSource.includes("!runtimeTransitioning && rewindState"), "source-session rewind content is isolated");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
