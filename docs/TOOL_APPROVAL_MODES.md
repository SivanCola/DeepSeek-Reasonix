# Tool permissions: Ask, Auto, and Yolo

The Ask / Auto / Yolo control under the desktop composer sets how Reasonix handles tool permission approvals. It decides whether writing files, running commands, or calling permission-gated tools pauses for your approval first.

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
- `Enter` confirms the highlighted action. Tool approvals default to “Allow once”; plan confirmation defaults to “Start execution”.
- `1` / `2` / `3` / `4` select the matching numbered tool-approval action; plan confirmation only has `1` / `2` / `3`.
- `Esc` denies the current tool approval; in plan confirmation it means exit or keep planning.
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

### Failure recovery checkpoint (optional)

Auto can enable **report after failures**:

- On the success path there are no extra confirmations and no extra model calls.
- After a tool or verification failure, read-only diagnosis may continue; one host-proven same-strategy verification retry can run automatically.
- If the next write changes method, expands scope, or raises risk, a recovery card appears: **Continue this change** / **Revise the plan** / **Stop task**.
- **Continue** authorizes only the single write shown on the card. **Revise** refuses that write and injects your follow-up requirements. **Stop** cancels the root agent and current-task sub-agents.
- Effective only in Auto. Ask / Yolo keep the preference but do not apply it.
- New sessions default **on**. Pre-upgrade sessions missing the field default **off**.

This is not the same as plan confirmation: plan confirmation decides whether to start execution; the failure recovery checkpoint handles strategy changes during execution.

## Yolo mode

Yolo maximizes continuous execution. Ordinary tool permission prompts are skipped so writes and commands interrupt less.

### How to enable

- Click **Yolo** in the tool-permission control.
- Or toggle with `Ctrl+Y` / `Cmd+Y`.
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

- Prefer **Auto** for trusted day-to-day work, with failure recovery **on** for new sessions.
- Use **Ask** when the workspace, data, or operation risk is unclear.
- Use **Yolo** only after the plan is confirmed and the tree is disposable or easily rolled back.
