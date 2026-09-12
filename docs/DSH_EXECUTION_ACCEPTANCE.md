# Harness migration acceptance report

Acceptance baseline: Reasonix `main-v2@986a6bc967`; behavioral reference: DeepSeek Harness `master@c291e7961a`. Validation date: 2026-09-13.

## Result

The harness-style execution model is the default and has no legacy behavior switch. Structured file mutations use host-owned live observations and version checks. Read coverage, proof receipts, operation settlement, Auto Guard, and unknown-effect recovery state no longer control tool admission or completion. Legacy fields and terminal states remain readable only for historical compatibility.

## Reported issue sequences

| Reports | Acceptance sequence | Result | Coverage |
| --- | --- | --- | --- |
| #9994, #9995, #10067, #10103 | Three `read → edit → bash → edit` cycles, including a Chinese path, CRLF, move, and delete | Passed; successful writes refresh observation and commands do not consume or freeze it | Agent integration, file tools, macOS, Windows 11 |
| #10053 | Read one window of a large file, run a command, and finish | Passed; no whole-file debt, forced continuation, or final gate | Agent integration and bounded-read tests |
| #10085 | Same-batch `read → edit → edit → bash` | Passed; observations update in actual execution order | Agent batch integration, macOS, Windows 11 |
| #10153 | Simulate a committed side effect whose result is lost, then inspect state and continue | Passed; the host records `unknown`, closes the turn as ordinary `interrupted`, requires no user decision, blocks no network or identical call, and performs no replay | Controller crash test, turn ledger, Desktop compatibility API |

## Freshness and concurrency

- Unobserved overwrite returns `FS_NOT_OBSERVED`; confirmed absence permits only a no-overwrite create.
- Any successful text window observes the current version, and each successful write advances it.
- Same-size rewrites, restored mtime, permission changes, replacements, and aliases invalidate stale observations.
- Competing writers from one version cannot silently lose an update; concurrent creates cannot overwrite the winner.
- Local publication retains temporary files, atomic replacement, permissions, and encoding. Buffer and disk targets have distinct identities.
- `read_file`, `write_file`, `edit_file`, `multi_edit`, `notebook_edit`, `delete_range`, `delete_symbol`, and `move_file` use the shared observation or post-commit state path.

## Recovery, completion, and compatibility

- Tool start is persisted before the body runs. Started calls settle on cancellation and calls that never start receive explicit results.
- Restart recovery pairs a missing result with one `unknown` result. Current runs emit neither `recovery_required` nor `requires_user_decision`; old values still decode and render read-only.
- Reconstructed context includes one bounded factual handoff and advisory state checks. Current file contents never synthesize success for the earlier call.
- `complete_step`, `review_report`, and read-policy receipts are absent from discovery. A legacy call receives an ordinary `tool_retired` result.
- Recovery action endpoints return `tool_recovery_retired` and cannot confirm, reject, inspect hidden arguments, or replay an operation.
- `todo_write` validates public fields, state values, hierarchy, and stable IDs. The model updates completion explicitly.
- Identical consecutive calls receive reminders only at counts 3, 5, and 8.

## Platform and product validation

| Environment | Validation | Result |
| --- | --- | --- |
| macOS | Full root Go tests and vet; independent Desktop and SDK Go modules; contract, golden, cache, and repository checks | Passed |
| Desktop frontend | Full frontend test suite and production build | Passed |
| Desktop shell | 206 shell tests and 57 Electron layout scenarios | Passed |
| Windows 11 VM | Native volume serial/file index/handle identity; same-size changes; real ACL `ChangeTime`; observation, concurrent create/edit, and harness scenarios | Passed |
| Desktop compatibility | Legacy recovery cards are read-only with no actions; current cards retain not-started/failed/interrupted/unknown facts; normal input remains available | Passed |

Windows validation ran natively in the local Parallels Windows 11 VM rather than through cross-compilation. The test copy and temporary artifacts were removed afterward.

## Removed and retained systems

Removed code includes whole-file read debt, frozen batch evidence, source-token authorization, anchor-range shadow evidence, operation prepared/applied/settled state, completion proof gates, the Auto Guard reviewer, recovery confirmation actions, repeated/no-progress rejection, and the unused shell proof preflight.

Goal, Plan approval, ordinary permissions, sandboxing, checkpoints, multi-agent execution, tool-call pairing, and factual execution display remain. Legacy provider/session fields, the `recovery_required` enum, and frontend recognition exist only to read old history; current execution does not write or activate them.

## Guarantee boundary

A window read means that version was observed; it is not whole-file review. Bash, MCP, and external programs do not grant file observations, and their file changes are detected by the next structured mutation. ACP has no conditional atomic-write API, and a local pre-publication check cannot constrain an external writer that ignores the process lock, so neither route promises universal cross-process CAS. Unknown external side effects have no host-level exactly-once guarantee.
