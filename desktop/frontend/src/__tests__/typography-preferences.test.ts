// Run: tsx src/__tests__/typography-preferences.test.ts

import {
  TYPOGRAPHY_REGION_META,
  createDefaultTypographyPreferences,
  fontStackForPreference,
  normalizeTypographyPreferences,
  sanitizeCustomFontName,
} from "../lib/typographyPreferences";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\nregional typography preferences");

const defaults = createDefaultTypographyPreferences();
eq(defaults.conversation.followGlobal, true, "regions follow global by default");
eq(defaults.code.fontSize, TYPOGRAPHY_REGION_META.code.baseSize, "code uses its semantic base size");

const normalized = normalizeTypographyPreferences({
  conversation: { followGlobal: false, fontFamily: "pingfang", fontSize: 99 },
  metadata: { followGlobal: false, fontFamily: "custom", customFontName: "  IBM   Plex Sans  ", fontSize: 8 },
});
eq(normalized.conversation.fontSize, TYPOGRAPHY_REGION_META.conversation.max, "oversized values clamp to the region maximum");
eq(normalized.metadata.fontSize, TYPOGRAPHY_REGION_META.metadata.min, "undersized values clamp to the region minimum");
eq(normalized.metadata.customFontName, "IBM Plex Sans", "custom names are normalized");
eq(normalized.interface.followGlobal, true, "missing regions retain backward-compatible defaults");

eq(sanitizeCustomFontName("Bad; font"), "", "unsafe CSS delimiters are rejected");
eq(fontStackForPreference(normalized.conversation).includes("PingFang SC"), true, "preset resolves to a usable font stack");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
