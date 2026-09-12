# Tool permissions: Ask, Auto, and Yolo

The Ask / Auto / Yolo control under the desktop composer sets how Reasonix handles tool permission approvals. All three modes stay visible so you can switch directly without relying on a shortcut or settings page.

Tool permission is independent of collaboration mode:

- **Collaboration mode** (Normal / Plan / Goal) decides how Reasonix advances the task. There is no automatic task mode or selectable quality floor. Verification obligations come from real tool actions, project rules, task risk, and explicit user requirements.
- **Tool permission** decides whether controlled tools wait for approval before running.

## Quick comparison

| Mode | Behavior | Good for | Not ideal for |
| --- | --- | --- | --- |
| Ask | Request approval before controlled tools (writes, commands, etc.). | Unfamiliar repos, high-risk edits, production-related work, step-by-step review. | Many low-risk repeated operations, or when you already trust continuous execution. |
| Auto | Auto-approve ordinary tool permissions, including interactive `remember`/`forget`; explicit `ask` / `deny` rules and plan confirmation still apply. Headless memory keeps the create-only boundary. | Daily code reading, small fixes, tests, normal implementation in a trusted workspace. | When you want every write or command confirmed by hand. |
| Yolo | Skip ordinary tool permission prompts so writes, commands, and `remember`/`forget` run with fewer interruptions; `deny` rules, plan confirmation, ask questions, and sandbox/config reviews still apply. | Temporary branches, roll-backable worktrees, bulk mechanical edits after a confirmed plan. | Production, sensitive files, delete/publish/push, or unclear requirements. |

## Ask mode

Ask is the most conservative tool-permission mode. When Reasonix needs approval for a tool call, an approval card appears so you can allow once, allow for the session, always allow, or deny.

Dynamic Bash never inherits a broader Bash, prefix, or glob rule. Parameter/arithmetic expansions,
assignments, redirects, heredocs, and globs still follow the normal mode
fallback, while nested/indirect execution requires this human approval path in
interactive Ask and Auto. Reusable choices save the identical complete command
as `Bash=<literal>`.

### Approval card shortcuts

- `←` / `→` cycle the highlighted action.
- `Enter` confirms the highlighted ordinary tool-approval action, which defaults to “Allow once”.
- `1` / `2` / `3` / `4` select the matching numbered ordinary tool-approval action.
- Plan confirmation has three direct actions: **Start execution** / **Revise plan** / **Exit without executing**. On Desktop, use one click or the matching number key. On CLI, use the matching number key or select a row and press `Enter`; `n` / `Esc` keeps planning for compatibility. Exiting rejects the pending plan and returns to Normal without starting an execution turn.
- Outside a pending CLI Plan confirmation, `Esc` stops the current task.
- If you `Tab` to a button and press `Enter`, that focused button runs (it is not overridden by the highlight).

Headless `reasonix run` has no approval card to answer. Its default Ask posture
therefore fails closed for writer fallback and explicit ask rules instead of
adding prompts or silently approving them. Use the existing `--auto` / `-y`
option when unattended automation should allow ordinary writer fallback; no
additional safety setting is required.

## Auto mode

Auto suits everyday development. It auto-approves ordinary tool permissions so you click less, but it is not unrestricted.

Auto still respects:

- Explicit `deny` rules.
- Explicit `ask` rules.
- Plan-mode “start execution” confirmation.
- Interactive `remember` and `forget` use the normal Auto fallback, so default
  calls proceed without a prompt while explicit `ask` / `deny` rules remain
  effective. Headless memory still keeps the bounded create-only exception and
  otherwise fails closed.
- Extending writable roots outside the workspace. Auto, Ask, and YOLO never grant a new directory without an explicit write-access card. File tools request the target parent directory automatically; Bash must pass `additional_write_dirs` plus a `justification`. Approving extends the sandbox write roots; it does not rerun the command unconfined.
- Human approval for nested or indirect Bash execution, even inside an approved-plan execution window. Guardian and allowing hooks cannot replace it; parameter/arithmetic expansions, assignments, redirects, and globs remain on Auto's fast path.
- MCP destructive calls when the effective policy is `auto`, `prompt`, or `writes`.
- Ask questions (never auto-answered).

### When Auto asks

Auto is designed as a behavior, not another feature to configure:

> Auto executes operations allowed by the permission policy. It asks only when a new plan, product tradeoff, or other genuinely user-owned decision appears.

- Workspace reads/writes, commands, source/config/workflow edits, dependencies, tests, and external operations follow the existing permission policy. Auto Guard no longer adds risk-based prompts.
- Consequently, default Auto does not ask merely because an operation is `git push`, publish, deploy, destructive, privileged, or global. Explicit `ask` / `deny`, sandbox, MCP, and tool-specific permission boundaries still apply.
- Creating and updating an ordinary task plan stays on the fast path. The model
  updates it with `todo_write`; no recovery reviewer or plan-transition card is
  inserted by Auto.
- A failure is an execution fact, not a task-wide permission boundary. Later
  calls perform their own permission, sandbox, and file-version checks. Retrying
  the same operation does not enter a recovery review.
- Timeouts receive concise failure-path guidance to inspect current state and partial effects before retrying. They do not ask the user to reset Auto, switch modes, or restart the session.
- Exact consecutive tool calls only receive reminders at counts 3, 5, and 8.
  They are never disabled and do not create an Episode or recovery pause.
- Headless runs fail closed when a genuine plan decision is required.
- Headless Ask/Auto/DontAsk also fail closed on nested or indirect Bash unless the identical `Bash=<literal>` was explicitly granted.
- Ask, Auto, and YOLO keep their permission semantics; no separate recovery
  safety setting exists.

Auto is not a filesystem snapshot or rollback mechanism. Use a clean Git branch or disposable worktree when changes must be reversible. Plan decides whether to start; Auto handles ordinary execution afterward.

The retired Auto Guard has no runtime gate, per-operation stop, Episode budget,
or reset ritual. Permission policy and the sandbox continue to own capability
boundaries.

## Yolo mode

Yolo maximizes continuous execution. Ordinary tool permission prompts are skipped so writes, commands, and memory remember/forget interrupt less.

Yolo is the only approval posture that may bypass the nested/indirect-Bash human
requirement and explicit interactive memory `ask` rules. Auto also skips the
default memory fallback prompt, but preserves explicit `ask` rules. Neither mode
bypasses explicit `deny` rules, the sandbox, plan confirmation, or managed config writes.

Because Yolo already opts out of confirmations, the CLI `/clear` command clears
the session immediately in Yolo mode instead of opening its confirmation
overlay; Ask, Auto, and Plan keep the confirmation.

### How to enable

- Select Yolo directly under the composer, choose it as the new-session default,
  or use the current binding shown in **Settings → Shortcuts** (`Ctrl+Y` /
  `Cmd+Y` by default).
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
