// Run: tsx src/__tests__/decision-surface.test.tsx
//
// Decision surfaces: ordinary approvals stay select-then-confirm while Plan
// and Auto boundary cards use immediate buttons; no double submit.

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import gsap from "gsap";
import { ApprovalModal } from "../components/ApprovalModal";
import { ClearContextCard } from "../components/ClearContextCard";
import { LocaleProvider } from "../lib/i18n";
import type { WireApproval } from "../lib/types";

let passed = 0;
let failed = 0;

type GsapToOptions = { onComplete?: () => void };
const gsapForTests = (typeof gsap.to === "function" ? gsap : (gsap as unknown as { default?: typeof gsap }).default) as unknown as {
  to?: (target: unknown, vars: GsapToOptions) => unknown;
};
if (typeof gsapForTests.to === "function") {
  gsapForTests.to = (_target: unknown, vars: GsapToOptions) => {
    vars.onComplete?.();
    return {};
  };
}

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function installDom(language = "en-US") {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: language });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  return dom;
}

console.log("\ndecision surface");

// Plan keeps one explicit start decision, but both visible choices are direct
// buttons. Exit/stop remain global controls rather than extra card branches.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const answers: Array<[boolean, boolean, boolean]> = [];
  const revisions: string[] = [];
  const approval: WireApproval = {
    id: "plan-1",
    tool: "exit_plan_mode",
    subject: "Plan ready",
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={(a, s, p) => answers.push([a, s, p])}
          onRevisePlan={(text) => revisions.push(text)}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  eq(actions.length, 2, "Plan has start and revise actions only");
  eq(document.querySelector(".prompt-shelf__actions")?.getAttribute("role"), "group", "Plan actions use button-group semantics");
  ok(actions.every((action) => action.getAttribute("role") === "button"), "Plan actions are announced as buttons");
  ok(!document.querySelector(".decision-confirm-bar__confirm"), "Plan has no redundant confirm button");
  ok(!document.body.textContent?.includes("Exit plan mode"), "Plan card hides the exit branch");
  ok(!document.body.textContent?.includes("Stop task"), "Plan card relies on the global Stop control");

  await act(async () => {
    actions[1].click();
    await flushTimers();
  });
  ok(document.querySelector(".plan-revision__input") != null, "Revise opens the inline editor in one click");
  eq(answers.length, 0, "Opening revision does not start execution");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const answers: Array<[boolean, boolean, boolean]> = [];

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={{ id: "plan-start", tool: "exit_plan_mode", subject: "Plan ready" }}
          onAnswer={(a, s, p) => answers.push([a, s, p])}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const start = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")]
    .find((action) => action.textContent?.includes("Start execution")) as HTMLButtonElement;
  await act(async () => {
    start.click();
    start.click();
    await flushTimers(220);
  });
  eq(answers.length, 1, "Plan starts with one click and ignores double submit");
  eq(JSON.stringify(answers[0]), JSON.stringify([true, false, false]), "Plan start approves execution once");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// Tool approval: click only selects; confirm submits once; double-confirm ignored.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const answers: Array<[boolean, boolean, boolean]> = [];
  const approval: WireApproval = {
    id: "bash-1",
    tool: "bash",
    subject: "ls -la",
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={(a, s, p) => answers.push([a, s, p])}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  eq(actions.length, 4, "ordinary tool approval has four options");
  ok(actions[0].classList.contains("prompt-action--selected"), "default selection is allow once");

  await act(async () => {
    actions[3].click();
    await flushTimers();
  });
  eq(answers.length, 0, "clicking deny only selects");
  ok(actions[3].classList.contains("prompt-action--selected"), "deny becomes selected");

  const confirm = document.querySelector(".decision-confirm-bar__confirm") as HTMLButtonElement;
  await act(async () => {
    confirm.click();
    confirm.click();
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flushTimers(220);
  });
  eq(answers.length, 1, "double click/enter submits only once");
  eq(JSON.stringify(answers[0]), JSON.stringify([false, false, false]), "deny maps to (false,false,false)");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// Auto reuses the decision shelf with one-click continue or revise. Task
// cancellation stays on the ordinary Stop control instead of becoming a third
// recovery-specific branch. Details stay collapsed; no select-then-confirm.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const decisions: Array<{ action: string; feedback?: string }> = [];
  const approval: WireApproval = {
    id: "guard-1",
    tool: "bash",
    subject: "git push origin feature",
    kind: "recovery",
    recovery: { next_action: "git push origin feature", change_kind: "risk", can_grant_task: true },
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={() => undefined}
          onResolveRecovery={(action, feedback) => decisions.push({ action, feedback })}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  eq(actions.length, 2, "Auto recovery has continue and try-another actions");
  eq(document.querySelector(".prompt-shelf__actions")?.getAttribute("role"), "group", "Auto boundary actions use button-group semantics");
  ok(actions.every((action) => action.getAttribute("role") === "button"), "Auto boundary actions are announced as buttons");
  ok(!actions.some((action) => action.textContent?.includes("Stop task")), "Auto recovery does not add a third Stop decision");
  ok(!document.body.textContent?.includes("Stop task"), "Auto boundary card relies on the global Stop control");
  ok(!document.querySelector(".decision-confirm-bar__confirm"), "Auto recovery has no select-then-confirm bar");
  ok(document.body.textContent?.includes("Confirm before execution"), "Auto boundary uses plain confirmation copy");
  ok(!document.body.textContent?.includes("Auto needs"), "Auto boundary hides the internal mechanism name");
  ok(!document.body.textContent?.includes("checkpoint"), "UI hides internal checkpoint terms");
  ok(!document.body.textContent?.includes("same_strategy"), "UI hides internal reviewer terms");
  ok(!document.querySelector(".recovery-details"), "details stay collapsed by default");
  ok(!document.body.textContent?.includes("Add guidance"), "optional guidance is removed from the default card");
  const taskGrant = document.querySelector(".recovery-task-grant input") as HTMLInputElement;
  ok(taskGrant, "bounded recovery offers a current-task semantic grant");
  ok(!taskGrant.checked, "task grant is opt-in");

  await act(async () => {
    actions[0].click();
    actions[0].click();
    await flushTimers(220);
  });
  eq(decisions.length, 1, "double click submits only once");
  eq(decisions[0]?.action, "continue", "continue resolves only the waiting action");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// The optional semantic grant is explicit and maps to a distinct backend
// action; it is never inferred from a raw command match.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const decisions: Array<{ action: string; feedback?: string }> = [];
  const approval: WireApproval = {
    id: "guard-task-grant",
    tool: "bash",
    subject: "git push origin feature",
    kind: "recovery",
    recovery: { next_action: "git push origin feature", change_kind: "risk", can_grant_task: true },
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={() => undefined}
          onResolveRecovery={(action, feedback) => decisions.push({ action, feedback })}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const taskGrant = document.querySelector(".recovery-task-grant input") as HTMLInputElement;
  const continueButton = document.querySelector(".prompt-shelf__actions .prompt-action") as HTMLButtonElement;
  await act(async () => {
    taskGrant.click();
    await flushTimers();
  });
  await act(async () => {
    continueButton.click();
    await flushTimers(220);
  });
  eq(decisions[0]?.action, "continue_task", "checked semantic grant uses the task-scoped recovery action");

  await act(async () => root.unmount());
  dom.window.close();
}

// Revise is one-click; optional empty feedback still works.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const decisions: Array<{ action: string; feedback?: string }> = [];
  const approval: WireApproval = {
    id: "guard-2",
    tool: "write_file",
    subject: "expand to b.go",
    kind: "recovery",
    recovery: { next_action: "edit b.go", change_kind: "scope", failed_summary: "a.go failed" },
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={() => undefined}
          onResolveRecovery={(action, feedback) => decisions.push({ action, feedback })}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  await act(async () => {
    actions[1].click();
    await flushTimers(220);
  });
  eq(decisions[0]?.action, "revise", "try another approach rejects immediately");
  ok(!decisions[0]?.feedback, "empty optional feedback is allowed");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// Destructive MCP approval is one-shot even though the tool name is dynamic.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const answers: Array<[boolean, boolean, boolean]> = [];
  const approval: WireApproval = {
    id: "mcp-danger-1",
    tool: "mcp__srv__wipe",
    subject: "MCP srv/wipe declares destructive side effects",
    reason: "This installed MCP tool declares destructive side effects. Review the target and arguments before allowing this call. Auto/YOLO approval cannot answer this decision.",
    fresh: true,
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={(a, s, p) => answers.push([a, s, p])}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  eq(actions.length, 2, "fresh destructive MCP approval only offers allow once and deny");
  ok(!document.body.textContent?.includes("Always allow"), "fresh destructive MCP approval hides remembered grants");

  const confirm = document.querySelector(".decision-confirm-bar__confirm") as HTMLButtonElement;
  await act(async () => {
    confirm.click();
    await flushTimers(220);
  });
  eq(JSON.stringify(answers[0]), JSON.stringify([true, false, false]), "fresh destructive MCP approval is one-shot");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// Clear context: default cancel; clear requires explicit confirm; Escape cancels.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let cancelled = 0;
  let confirmed = 0;

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ClearContextCard onCancel={() => { cancelled += 1; }} onConfirm={() => { confirmed += 1; }} />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  ok(actions[0].classList.contains("prompt-action--selected"), "clear context defaults to cancel");

  await act(async () => {
    actions[1].click();
    await flushTimers();
  });
  eq(confirmed, 0, "clicking clear only selects");
  ok(actions[1].classList.contains("prompt-action--selected"), "clear option becomes selected");

  await act(async () => {
    (document.querySelector(".decision-confirm-bar__confirm") as HTMLButtonElement).click();
    await flushTimers();
  });
  eq(confirmed, 1, "confirm runs clear once");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let cancelled = 0;

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ClearContextCard onCancel={() => { cancelled += 1; }} onConfirm={() => undefined} />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  eq(cancelled, 1, "Escape cancels clear context immediately");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// Composer decision host stays in the tree while visually hidden.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(
      <div>
        <div className="composer-decision-host composer-decision-host--hidden" hidden inert aria-hidden="true">
          <textarea id="composer-input" defaultValue="draft text" />
        </div>
      </div>,
    );
    await flushTimers();
  });

  const host = document.querySelector(".composer-decision-host") as HTMLElement;
  const input = document.getElementById("composer-input") as HTMLTextAreaElement;
  ok(host != null, "composer decision host remains mounted");
  ok(host.hasAttribute("hidden"), "host is hidden during decision");
  ok(host.hasAttribute("inert") || (host as HTMLElement & { inert?: boolean }).inert === true, "host is inert during decision");
  eq(input.value, "draft text", "draft value survives while host is hidden");
  eq(host.getAttribute("aria-hidden"), "true", "host is aria-hidden during decision");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// New approval id resets selection and submitting state.
{
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const answers: Array<[boolean, boolean, boolean]> = [];
  let approval: WireApproval = { id: "a1", tool: "bash", subject: "echo 1" };

  const paint = async (next: WireApproval) => {
    approval = next;
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ApprovalModal
            approval={approval}
            onAnswer={(a, s, p) => answers.push([a, s, p])}
            onStop={() => undefined}
          />
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };

  await paint(approval);
  const actions = () => [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  await act(async () => {
    actions()[3].click();
    await flushTimers();
  });
  ok(actions()[3].classList.contains("prompt-action--selected"), "deny selected on first prompt");

  await paint({ id: "a2", tool: "bash", subject: "echo 2" });
  ok(actions()[0].classList.contains("prompt-action--selected"), "new prompt id resets selection to allow once");
  eq(answers.length, 0, "selection reset does not submit");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
