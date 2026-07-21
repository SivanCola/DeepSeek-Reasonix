# Tool permissions: Ask, Auto, and Yolo

The Ask / Auto / Yolo control under the desktop composer sets how Reasonix handles tool permission approvals. All three modes stay visible so you can switch directly without relying on a shortcut or settings page.

Tool permission is independent of collaboration mode:

- **Collaboration / runtime mode** decides how Reasonix advances the task (lightweight, balanced, or delivery-first).
- **Tool permission** decides whether controlled tools wait for approval before running.

## Quick comparison

| Mode | Behavior | Good for | Not ideal for |
| --- | --- | --- | --- |
| Ask | Request approval before controlled tools (writes, commands, etc.). | Unfamiliar repos, high-risk edits, production-related work, step-by-step review. | Many low-risk repeated operations, or when you already trust continuous execution. |
| Auto | Auto-approve ordinary tool permissions; explicit `ask` / `deny` rules, plan confirmation, and memory write/delete still apply. | Daily code reading, small fixes, tests, normal implementation in a trusted workspace. | When you want every write or command confirmed by hand. |
| Yolo | Skip ordinary tool permission prompts so writes and commands run with fewer interruptions; `deny` rules, plan confirmation, ask questions, and forced fresh approvals still apply. | Temporary branches, roll-backable worktrees, bulk mechanical edits after a confirmed plan. | Production, sensitive files, delete/publish/push, or unclear requirements. |

## Ask mode

Ask is the most conservative tool-permission mode. When Reasonix needs approval for a tool call, an approval card appears so you can allow once, allow for the session, always allow, or deny.

### Approval card shortcuts

- `←` / `→` cycle the highlighted action.
- `Enter` confirms the highlighted ordinary tool-approval action, which defaults to “Allow once”.
- `1` / `2` / `3` / `4` select the matching numbered ordinary tool-approval action.
- Plan confirmation has two direct actions: **Start execution** / **Revise plan**. They run with one click or the matching number key; there is no second Confirm click.
- `Esc` stops the current task.
- If you `Tab` to a button and press `Enter`, that focused button runs (it is not overridden by the highlight).

## Auto mode

Auto suits everyday development. It auto-approves ordinary tool permissions so you click less, but it is not unrestricted.

Auto still respects:

- Explicit `deny` rules.
- Explicit `ask` rules.
- Plan-mode “start execution” confirmation.
- Fresh human approval for memory write/delete (`remember` / `forget`).
- MCP destructive calls when the effective policy is `auto`, `prompt`, or `writes`.
- Ask questions (never auto-answered).

### When Auto asks

Auto is designed as a behavior, not another feature to configure:

> Auto handles reversible workspace work automatically. It asks only before a destructive, external, global, or otherwise explicit human-decision boundary.

- Workspace reads, source/config/workflow edits, project-local dependency changes, tests, and retries stay on the fast path.
- A change in implementation strategy or file scope inside the same task is handled automatically; it is not a user decision by itself.
- Destructive commands, remote push/publish/deploy, privilege escalation, system/global installs, and writes outside ordinary workspace policy still require confirmation.
- After a failure, read-only diagnosis and low-risk recovery continue automatically. Three consecutive failed attempts, or three reviewer-rejected alternatives, escalate to the user.
- Reviewer unavailability does not turn low-risk work into a prompt; deterministic hard boundaries still fail closed.
- When confirmation is required, one card shows the next action with two one-click choices: **Continue** / **Try another approach**. For host-classified bounded operations, the Continue choice can also remember the exact displayed operation and target for the current task. Technical details stay collapsed. Whole-task cancellation remains the global Stop control.
- **Continue** alone authorizes only the waiting call. The optional current-task grant matches the operation type and exact target boundary rather than raw command text; for example, a Git push grant includes both the remote and destination ref. Ambiguous targets, broader targets, behavior-changing options, force/destructive variants, and risk escalation ask again. It is never a permanent grant and is dropped when the task scope changes, on restart, or on session switch.
- Headless runs fail closed when a human decision is required.
- These boundaries are effective only in Auto. Ask and YOLO keep their existing approval semantics, and there is no separate safety setting to learn.

Auto is not a filesystem snapshot or rollback mechanism. Use a clean Git branch or disposable worktree when changes must be reversible. Plan decides whether to start; Auto handles ordinary execution afterward.

## Yolo mode

Yolo maximizes continuous execution. Ordinary tool permission prompts are skipped so writes and commands interrupt less.

### How to enable

- Select Yolo directly under the composer, choose it as the new-session default, or toggle with `Ctrl+Y` / `Cmd+Y`.
- Select Ask or Auto directly to leave Yolo.
- When entered via shortcut, Reasonix remembers the previous Ask/Auto baseline and restores it on the next toggle.

## Combining with collaboration modes

| Combination | Behavior |
| --- | --- |
| Plan + Ask | While planning, gated calls wait; after plan approval, ordinary writer fallback is auto-allowed, but explicit `ask` / `deny`, MCP `prompt` / `writes`, and forced fresh approvals still apply. |
| Plan + Auto | Plan confirmation still needs you; after start, ordinary tool permissions auto-approve. |
| Plan + Yolo | Plan confirmation still needs you; after start, ordinary tool prompts are minimized. |
| Goal + Ask | The goal keeps advancing but tool approvals still pause for you. |
| Goal + Auto | Best for most daily goal work: continuous progress with explicit rule boundaries. |
| Goal + Yolo | For very clear, roll-backable goal work; highest risk. |

## Recommended defaults

- Prefer **Auto** for trusted day-to-day work.
- Use **Ask** when the workspace, data, or operation risk is unclear.
- Use **Yolo** only after the plan is confirmed and the tree is disposable or easily rolled back.
