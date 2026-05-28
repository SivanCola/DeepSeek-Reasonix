import { formatGoalStatus, loadGoalState } from "../../../../goal/state.js";
import type { SlashHandler } from "../dispatch.js";

const goal: SlashHandler = (args, _loop, ctx) => {
  const root = ctx.codeRoot ?? ctx.memoryRoot ?? process.cwd();
  const first = (args[0] ?? "").toLowerCase();

  if (args.length === 0 || first === "status") {
    const loaded = loadGoalState(root);
    return { info: formatGoalStatus(loaded.state, loaded.error) };
  }

  if (first === "resume") {
    const loaded = loadGoalState(root);
    if (loaded.error) return { info: `Goal state could not be read: ${loaded.error}` };
    if (!loaded.state || loaded.state.status !== "running") {
      return { info: "No running Goal to resume." };
    }
    return {
      info: `Resuming Goal: ${loaded.state.goal}`,
      goal: { kind: "resume" },
    };
  }

  if (first === "stop" || first === "cancel") {
    return {
      info: "Stopping active Goal.",
      goal: { kind: "stop" },
    };
  }

  const text = args.join(" ").trim();
  if (!text) return { info: formatGoalStatus(null) };
  return {
    info: `Starting Goal: ${text}`,
    goal: { kind: "start", goal: text },
  };
};

export const handlers: Record<string, SlashHandler> = {
  goal,
};
