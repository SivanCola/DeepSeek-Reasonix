---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-26
---

# The user's team of agents

## 1. Purpose

Studio keeps a team of long-lived agents beside the user's projects.

- Each agent has a name, a role, standing instructions, a look and its own conversation.
- A main agent (Chief) creates and manages the others, and agents hand work to each other.
- Group chats let several agents take turns on one thread.

| ID | Principle |
| --- | --- |
| T1 | A message between agents carries no authority. The receiver runs under its own approval mode, held to the strictest mode on the chain that handed the work on. |
| T2 | Identity is dynamic state. It rides the turn tail as standing blocks; the system prompt and tool block are byte-identical across every agent session. |
| T3 | Judgements read structure. Members pass or hand on a turn with tools; the user's mentions are member ids. Nothing parses prose for `[PASS]` or `@Name`. |
| T4 | How a turn ended is the host's word. A failed, stopped or interrupted turn answers with that status and its code. |
| T5 | Permission modes, main agents, relay limits and deletion are the user's alone, through the window. No tool reaches them. |

## 2. Layers

| Package | Role |
| --- | --- |
| `internal/state/teamroster` | Leaf store: roster (agents, groups), exchange book, group logs, round state. Atomic writes, revision CAS, a writer lease per process. |
| `internal/session/team` | Service: messaging and the handoff tree, group rounds and timers, user administration, deterministic rendering. Drives sessions only through `Panes`. |
| `internal/session/control` | The seat a session speaks for: standing blocks, team tools' caller context, delivery into the inbox, settlement at turn end, the approval ceiling and the `team_change` card. |
| `internal/frontend/serve` | `Panes` over hub runtimes (background seats, idle close, live cap), the `/team` routes and view, and keeping the team out of the project tree. |
| `desktop/frontend-next/src/ui/team` | Sidebar block, agent and group dialogs, group chat stage, origin rows in the transcript. |

## 3. Messaging

| Step | What happens |
| --- | --- |
| Send | `message_agent` opens an exchange whose parent is the exchange the caller is serving, then queues a framed `<agent-message>` as a host turn in the receiver's inbox. |
| Refuse | Work handed upstream on its own branch (`team.upstream`), a chain six deep, sixteen unanswered, twelve a minute, eight in one turn. |
| Answer | When the receiver's turn ends, the controller reports what it served and read. With work still out, the answer is held; otherwise `<agent-reply>` goes to the sender's seat. |
| Read | A reply counts as returned only once the sender's turn has read it, so an answer never leaves before the replies it depends on. |
| Receipts | Team tools declare every effect host-owned (`tool.HostStateWriter`): a turn that only managed the team reports no unverified change to the work product. |

## 4. Group rounds

| Rule | Behaviour |
| --- | --- |
| Who answers | The mentioned members, else everyone or the admin by the group's mode. Members speak one at a time. |
| Handing on | `hand_turn` puts named members next and counts as a relay. |
| Relay limit | At 3 × members by default, agent calls park behind a Continue the user gives. |
| Timers | A speaker silent for 240 s is missed; a tool running 15 s moves the member off the order and its answer posts late. |
| Restart | The turn in flight is cut and the rest are parked. |

## 5. Approvals

| Change | Approval |
| --- | --- |
| A turn delivered by the team | Gated at the stricter of the session's mode and the chain's ceiling; a forged ceiling can only lower it. |
| A model choosing an agent's folder | Fresh `team_change` card every time; no posture, grant, hook or rule answers it. |
| Main agent, permission, relay limit, deletion | User only, through `/team` routes. |

Delegated work inherits the effective turn ceiling. Strict read-only workers
cannot dispatch team work or mutate team state, including through deferred tools.
Roster, schedule and secret management routes are host-only for paired devices.

委派任务继承当前回合的有效审批上限。严格只读的工作 Agent 不能调度团队工作或修改团队状态，延迟加载工具也遵守此边界。配对设备不能调用仅限主机的名册、定时任务和密钥管理接口。

## 6. Memory and schedules

| Feature | Behaviour |
| --- | --- |
| Memory tiers | Each agent keeps its own memory; `remember_shared` and `recall_shared` reach the team's and a group's. Shared memory rides the turn tail as `team-memory`. |
| Memory review | After a turn the user spoke in, the title model reviews it and saves or forgets facts. It runs 2 s after the turn, and the team settings turn it off. |
| Schedules | A prompt for an agent or a group: once, daily, weekly, or every 10 minutes to 7 days. A slot missed by more than 2 minutes is skipped, not made up. |
| Who fires them | The Studio host that holds the team's writer lease, on its tick. A fired run is gated at the agent's own approval mode. |
| Browser | Each agent's browser runs on its own profile (`agent-<id>`), in every seat, so one agent's logins are never another's. |
| Usage | Every call from an agent's seat, its memory review included, is recorded under the agent; the usage page lists spend by agent, and the agent's drawer sums its conversations. |

`remember_shared` requires fresh human approval. Automatic review preserves
pinned guidance and checks the observed revision before editing or archiving a
fact. Shutdown cancels and joins reviews before releasing the team's writer lease.

`remember_shared` 每次都需要人工审批。自动整理保留固定指导，修改或归档事实前检查读取时的版本；主机关闭时先取消并等待整理退出，再释放团队写入租约。

## 7. Secrets

An agent asks with `request_secret`, from its own conversation only. The user
answers on a card: the value, and which agents get it. The asker alone is the
default, and a secret already saved under the name keeps the agents it had.

| Rule | Behaviour |
| --- | --- |
| Reaching commands | Every agent given the secret reads it in its shell commands as `$REASONIX_SECRET_NAME`. No tool returns the value. |
| Reaching models | Every built-in adapter masks the request it sends (`provider.MaskOutbound`), and so does every crossing into an extension process (`providerconv`). A model reads `[secret:NAME]`, or `[secret:A,B]` for a value saved under two names. |
| Signed bytes | Kept whole only by the adapter that sends their signature back: Anthropic thinking, Gemini thought steps, thought-signed calls to Gemini endpoints. Every other receiver gets them masked. |
| Freshness | The mask reads the secrets file on every snapshot (`secrets.WatchValues`), in every process that builds a session or a provider, and holds each value it has seen until the process exits. |
| Delete and replace | The value stops reaching commands and is kept as retired, so copies already in conversations stay masked. Forgetting retired values is a separate, confirmed action. |
| Writing back | The file tools and their previews refuse text carrying a mark (`secret.masked_write`); so do shell commands (`secret.masked_command`) and MCP calls, proxied ones included (`secret.masked_call`). |
| What is kept | Every tool's result, a command's failure tail and progress, and a background job's log are masked before they are drawn or stored; a value split across two writes is held back until it can be recognised. |

JSON masking inspects decoded string and numeric tokens, including object keys
and duplicate keys. Tool descriptions and schemas cross the same boundary.

JSON with no matching value retains its exact bytes; a prefix containing a
registered value must change to remove it. Output streams refresh their mask
on every write so a newly saved value has no timed cache exemption.

JSON 脱敏检查解码后的字符串和数字，包括对象键名及重复键；工具说明和 schema 同样脱敏。没有命中密钥的 JSON 保持原始字节；前缀含有已登记密钥时，必须改变字节以去除它。输出流每次写入都刷新脱敏状态，新保存的密钥不存在定时缓存豁免。

## 8. Known limits

| Limit | Note |
| --- | --- |
| Windows | No OS sandbox bounds what an agent's shell can write, so the roster file is protected only by being outside workspace write roots on macOS and Linux. |
| Secrets at rest | Stored unencrypted. On macOS and Linux with the sandbox on, agents' commands are denied the file; on Windows or with the sandbox off they can read it, and the card says so before a value is typed. |
| Secrets in commands | A command can read a value and print it transformed (base64, hex); the mask matches the value, not what was done to it. Give a secret only to agents you trust. |
| Secret scope | "Only Ada" governs which commands receive the variable, in all of Ada's seats, groups included. Session files are not isolated: another agent's commands can read Ada's, where what the user typed is kept as typed. |
| Shared folders | An agent's checks see only its own writes: another agent working in a folder they share can change what those checks rely on without either receipt saying so. |
| Signed bytes elsewhere | A brokered or extension-hosted model that verifies signed bytes holding a value can refuse the turn, since those bytes reach it masked. |
| Short values | Values under 8 bytes are masked only between non-word characters. |
| Memory review | Runs on the title model, which reads up to 3000 characters of each of the user's lines in the turn. |
| Mentions with spaces | A reference token ends at a space, so in "@Ada Lovelace" only "@Ada" is read, and it is looked up as an @file reference rather than taken out as a mention. |
| Terminal | `/team` lists the team and `/msg` queues a message, delivered when a Studio host holding the team ticks. ACP seats no session on the team. |
