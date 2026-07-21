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

### Auto Guard

Auto includes **Auto Guard**, a host-side safety boundary:

- Ordinary workspace reads and edits stay on the fast path with no extra model calls.
- Deterministically high-risk mutations are checked before execution, even when no earlier tool failed. This includes destructive operations, dependency/configuration changes, installs, external mutations, and publish/push-style commands.
- After a tool or verification failure, read-only diagnosis may continue; one host-proven same-strategy verification retry can run automatically.
- After a failure, an isolated reviewer evaluates ambiguous changes. A rejection is first returned to the same root or sub-agent with the reason so it can diagnose or narrow the action; three consecutive rejected proposals escalate to a human.
- High-risk, expanded-scope, explicit strategy changes, repeated recovery failures, and reviewer escalation show one card: **Continue once** / **Revise action**. Whole-task cancellation remains the ordinary Stop control.
- **Continue once** authorizes only the waiting call. Grants and stale cards are never replayed after a restart; the next call is classified again.
- Headless runs fail closed when a human decision is required.
- Effective only in Auto. The legacy `auto_recovery_checkpoint = "off"` setting remains as an advanced compatibility kill switch.

Auto Guard is not a filesystem checkpoint or rollback mechanism. Use a clean Git branch or disposable worktree when changes must be reversible. Plan confirmation decides whether to start execution; Auto Guard evaluates action boundaries during execution.

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

- Prefer **Auto** with built-in Auto Guard for trusted day-to-day work.
- Use **Ask** when the workspace, data, or operation risk is unclear.
- Use **Yolo** only after the plan is confirmed and the tree is disposable or easily rolled back.
