import type { StructuredInvocationSubmit } from "./invocationDisplay";

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
  await applyGoal(goal);
  await send(
    goal,
    structured ? submitText.trim() : `/goal ${submitText.trim()}`,
    structured,
  );
}
