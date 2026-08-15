// Run: tsx src/__tests__/context-budget-card.test.ts

import { contextBudgetRecoveryKey, contextBudgetSourceKey, resolveContextBudget } from "../components/ContextBudgetCard";
import type { ContextInfo, ContextPanelInfo } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

console.log("\ncontext budget card");

eq(contextBudgetSourceKey("official"), "context.budgetSourceOfficial", "official source key");
eq(contextBudgetSourceKey("learned"), "context.budgetSourceLearned", "learned source key");
eq(contextBudgetSourceKey("nope"), "context.budgetSourceUnknown", "unknown source key");
eq(contextBudgetRecoveryKey("proactive_clip"), "context.budgetRecoveryClip", "clip recovery");
eq(contextBudgetRecoveryKey("none"), "", "none recovery is empty");

const fromPanel = resolveContextBudget(undefined, {
  contextBudget: { source: "official", windowTokens: 1000 },
} as ContextPanelInfo);
eq(fromPanel?.source, "official", "panel budget is preferred");

const fromContext = resolveContextBudget({
  used: 1,
  window: 2,
  sessionTokens: 3,
  contextBudget: { source: "learned" },
} as ContextInfo, null);
eq(fromContext?.source, "learned", "context info budget is used when panel omits it");

if (failed > 0) {
  process.exit(1);
}
console.log(`  ${passed} passed`);
