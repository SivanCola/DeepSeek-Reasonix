// Run: tsx src/__tests__/turn-actions-rendering.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nturn actions rendering");

const creationTranscriptRule = styles.match(
  /\.app--creation \.transcript\.transcript--creation-scrollbar,\s*:root\[data-theme-style\] \.app--creation \.transcript\.transcript--creation-scrollbar\s*\{([^}]+)\}/,
)?.[1] ?? "";

ok(
  /background-color:\s*var\(--bg\);/.test(creationTranscriptRule),
  "creation scrollbar layer paints an opaque backdrop so removed turn actions repaint (#6359)",
);

ok(
  /scrollbar-width:\s*none;/.test(creationTranscriptRule),
  "opaque backdrop preserves the custom Creation scrollbar contract",
);

const windowsTranscriptRule = styles.match(
  /:root\[data-platform="windows"\] \.app \.transcript\s*\{([^}]+)\}/,
)?.[1] ?? "";
const windowsThemeTranscriptRule = styles.match(
  /:root\[data-platform="windows"\]\[data-theme-pack\]\[data-theme-has-bg="true"\] \.app:not\(\.app--creation\) \.transcript\s*\{([^}]+)\}/,
)?.[1] ?? "";
const windowsTaskTranscriptRule = styles.match(
  /:root\[data-platform="windows"\]\[data-theme-pack\]\[data-theme-has-bg="true"\]\[data-theme-scene="task"\] \.app:not\(\.app--creation\) \.transcript\s*\{([^}]+)\}/,
)?.[1] ?? "";
const lastTransparentTranscriptRule = styles.lastIndexOf(
  '.app:not(.app--creation) .transcript-shell {\n  background: transparent !important;',
);
const windowsTranscriptRuleIndex = styles.indexOf(
  ':root[data-platform="windows"] .app .transcript {',
);

ok(
  /background-color:\s*Canvas\s*!important;/.test(windowsTranscriptRule) &&
    /linear-gradient\(var\(--chat-bg,\s*var\(--bg\)\)/.test(windowsTranscriptRule) &&
    /linear-gradient\(var\(--bg\),\s*var\(--bg\)\)\s*!important;/.test(windowsTranscriptRule),
  "Windows Classic, Workbench, and Creation transcripts paint an opaque repaint backing (#7011)",
);

ok(
  windowsTranscriptRuleIndex > lastTransparentTranscriptRule,
  "Windows repaint backing wins over later theme-pack transparency",
);

ok(
  /background-color:\s*Canvas\s*!important;/.test(windowsThemeTranscriptRule) &&
    /background-image:[\s\S]*var\(--windows-transcript-scene-image\)/.test(windowsThemeTranscriptRule) &&
    /background-attachment:[\s\S]*fixed[\s\S]*!important;/.test(windowsThemeTranscriptRule),
  "Windows background themes are composited on the transcript instead of through a transparent scroll layer",
);

ok(
  /--windows-transcript-scene-image:\s*var\(--theme-bg-task-image/.test(windowsTaskTranscriptRule) &&
    /--windows-transcript-pane-alpha:\s*var\(--theme-pane-task-alpha/.test(windowsTaskTranscriptRule),
  "Windows task scene uses its task image and pane opacity on the transcript layer",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
