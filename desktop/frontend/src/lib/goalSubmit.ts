import type { StructuredInvocationSubmit } from "./invocationDisplay";

/**
 * Activate a Goal, then submit the first turn.
 *
 * For structured Skill/Subagent submissions there is no `/goal` prose fallback:
 * if Goal activation fails, this must not call `send` (the Skill would otherwise
 * run without an active Goal). Callers should let activation errors propagate.
 */
export async function activateGoalAndSubmit({
  displayText,
  submitText,
  structured,
  applyGoal,
  send,
}: {
  displayText: string;
  submitText: string;
  structured?: StructuredInvocationSubmit;
  applyGoal: (goal: string) => void | Promise<void>;
  send: (displayText: string, submitText: string, structured?: StructuredInvocationSubmit) => void | Promise<void>;
}): Promise<void> {
  const goal = displayText.trim();
  // Fail closed: structured paths have no `/goal` wrap, so a no-op or rejected
  // activation must abort before SubmitInvocationsToTab.
  await applyGoal(goal);
  await send(
    goal,
    structured ? submitText.trim() : `/goal ${submitText.trim()}`,
    structured,
  );
}

/**
 * Tab-scoped first Goal turn. Captures `tabId` once so a later active-tab switch
 * cannot retarget SetGoalForTab or the structured Skill submit.
 */
export async function activateGoalAndSubmitOnTab({
  tabId,
  displayText,
  submitText,
  structured,
  setGoalForTab,
  sendToTab,
}: {
  tabId: string;
  displayText: string;
  submitText: string;
  structured?: StructuredInvocationSubmit;
  setGoalForTab: (tabId: string, goal: string) => void | Promise<void>;
  sendToTab: (
    tabId: string,
    displayText: string,
    submitText: string,
    structured?: StructuredInvocationSubmit,
  ) => void | Promise<void>;
}): Promise<void> {
  const sourceTabId = tabId;
  await activateGoalAndSubmit({
    displayText,
    submitText,
    structured,
    applyGoal: (goal) => setGoalForTab(sourceTabId, goal),
    send: (display, routedSubmit, routedStructured) =>
      sendToTab(sourceTabId, display, routedSubmit, routedStructured),
  });
}
