# DSH runtime alignment

This document fixes the executable contracts for the Reasonix runtime
alignment. It is normative for new sessions; legacy fields are display-only.

## Todo contract

`todo_write` accepts one object containing a required `todos` array. Every item
contains exactly `content` and `status`. Content is trimmed and must be non-empty
and unique within the replacement list. Status is one of `pending`,
`in_progress`, or `completed`. The complete array replaces the previous array;
`[]` is a valid clear. Ordering, deletion, replanning, pending-only lists, and
multiple in-progress items are valid.

The successful result is canonical JSON containing the normalized full list and
counts. Validation failure leaves the prior state unchanged. Result truncation
or duplicate presentation must never replace this state-bearing result.

Todo lifetime is one real host turn. A committed top-level turn start clears the
current list. Tool rounds, compaction, steering, approval answers, and Ask
answers stay in the same turn and do not clear it. A completed, failed, or
cancelled turn retains its last successful list for display until the next turn
starts. Goal continuation is a new turn and starts empty.

`complete_step`, hierarchical levels, `activeForm`, `step_id`, host advancement,
approved-plan seeding, Goal restoration, and completed-prefix protection are
retired execution behavior. The hidden `complete_step` tombstone only returns an
actionable retirement error. Old transcript records remain readable.

## Runtime ownership

The controller runtime is the sole owner of foreground admission, cancellation,
the active turn identity, and pending interactions. Durable turn events record
facts and never recreate a live executor after process restart. Runtime phases
are `idle`, `executing`, `cancelling`, `finishing`, `recovery_required`, and
`closed`; compatibility booleans are derived from the phase and current owners.

Stop is session-scoped. A supplied UI turn id is diagnostic only and cannot be a
precondition for cancellation. Cancellation signals the captured controller
before storage, notification, or callback cleanup. The existing 15-second tool
straggler grace applies to owned work. A turn whose owned work cannot converge
is represented as `recovery_required`; late work cannot resume that turn or
commit a newer runtime generation.

Ask, approval, Plan, recovery, and MCP decisions use `PendingPromptOwner` as one
registry. Every identity binds prompt id, kind, turn, and runtime epoch. Resolve
is single-winner and rejects stale runtime or turn identities. Callbacks run
outside the registry lock. Cancellation drains every registered prompt, and the
runtime snapshot derives `pendingPrompt` from this registry rather than an
approval-only side channel.
Resolution events preserve `answered`, `rejected`, or `cancelled`. A Plan
resolution and its Plan state are one logical batch. Turn termination closes any
remaining requests, so a restart cannot recreate answerable authority.

## Capability catalog

`skill.Store.Snapshot` is the discovery boundary. A generation has immutable,
stable-order candidates and an O(1) name index. Concurrent cold callers share a
single discovery scan; cancelled waiters do not cancel a scan needed by other
callers. Invalidation retains the previous complete generation until the new
one is complete, and refresh retries are bounded to two generations. Create,
update, delete, and detected root changes invalidate the generation.

`use_capability search` consumes one catalog and one MCP schema snapshot. Skill
argument lookup uses the store index and cannot rescan roots per result. List is
paged at 50 entries by default and 100 maximum. Its cursor binds to the catalog
fingerprint; a changed catalog rejects the cursor and requires a restart.
Discovery never connects an MCP server or calls `tools/list`.

## Compatibility and cache boundary

The changed todo schema and capability pagination are one intentional stable
prefix revision. Within the new contract, tool order, JSON schema bytes, and
the delivery marker stay deterministic; runtime state, timestamps, catalog
generations, and todo contents never enter the system prompt.

Legacy todo fields, completion declarations, Goal todo payloads, and dismissal
records are retained only so existing history can be shown. Continuing work
does not activate them. New Goal state writes no todo payload, approved Plans do
not generate todo calls, and the frontend keeps dismissal only as an in-memory
view preference.

## v3 session boundary

`internal/sessionv3` defines the final linear `reasonix.session.linear/v3`
event codec. The retired `reasonix.session.events/v3` prototype cannot be
opened directly and must pass through the restricted importer. Unknown required
events, damaged complete records, and unexplained projection operations remain
read-only and cannot resume execution. A session has one active write handle. Fork and edit-resend create an
independent child session instead of adding writable heads to one log. One
physical JSONL record contains one complete logical batch with contiguous event
sequences. Cold reads omit an unterminated tail. After acquiring the exclusive
lease, a writer preserves its original bytes and truncates back to the last
complete batch before restart recovery. Complete malformed records, gaps,
unknown codecs, and unknown required events fail closed.

Create returns only after the in-memory Session, writer handle, and immutable
session ID have been published. `session/title` and `session/config` are the
authoritative title and model-selection facts; the latter includes the
connection revision. Rebuilding an Agent replaces model context and appends
configuration without creating another Controller that competes for the same
writer. Directory indexes and legacy model sidecars are not v3 state sources.

`Append` means the live session accepted a fact. It validates the whole batch,
assigns sequences, retains an immutable copy, updates the in-memory projection,
and publishes to observers. It does not imply durability. The first pending
event starts a fixed 200 ms write-behind window; later appends do not extend the
deadline, and each handle has one drain chain. A background write failure keeps
the original ordered batch and pauses automatic retry. The next explicit
`Flush` retries safely. An uncertain write or fsync result becomes an explicit
uncertain persistence state and never causes a tool rerun.

The agent flushes before every model adapter call and before entering a
top-level tool body. A failed checkpoint prevents the downstream call. Todo,
approval, assistant-message, and `turn/end` appends do not force individual
flushes. Idle is not a durability guarantee. Export, cold-disk verification,
writer handoff, and clean shutdown wait for an explicit flush. Live snapshots
carry both event and durable sequences, and the former may be newer.

The new root is `sessions-v3`. Legacy migration first acquires the source write
lease, freezes the transcript and known sidecars with their size and digest,
builds and validates a v3 session in a same-filesystem temporary directory, and
publishes it by atomic rename. The canonical source path, head, digest, and target codec determine
the target ID, so identical input is idempotent and changed input creates a new
target. Original artifacts are copied byte-for-byte under `legacy/`; unknown
content is never decoded and re-encoded. Goal import excludes todos and
automatic continuation. Old unfinished runtime and approval records remain
history and never recreate live authority.

Each reachable head in a legacy schema-2 log migrates to a separate linear
session. `legacyHeadId` participates in both the deterministic target ID and the
migration-map key. Cold history pages validate records as a stream and stop at
the requested page boundary rather than materializing the complete log.

At the final coordinated cutover, Desktop host RPC moves to protocol version 4.
The Electron shell sends and validates the version from its embedded command
contract, so shell and service cannot drift through separately maintained
constants. Serve advertises `execution-v2`, `session-events-v3`, and
`session-identity-v1`. New Desktop builds reject execution control against a
remote missing any capability instead of emulating the new state machine over
old RPCs or path identities.

## Regression matrix

- Whole-list tests cover empty, pending-only, multiple in-progress, out-of-order
  completion, trimming, duplicate content, unknown fields, and invalid status.
- Replay tests must cover equal counts with different statuses, duplicate output
  presentation, compaction, branch isolation, and a new-turn clear.
- Catalog tests use 1,154 and 10,000 candidates and assert scan/read counts:
  exactly one shared cold scan, zero warm scans, and O(1) indexed skill lookup.
- Cancellation tests use channels as ordering barriers for model streams,
  serial and parallel tools, prompt publication, answer/cancel races, hooks,
  discovery, and compaction. Time-based sleeps are not correctness evidence.
- Runtime tests assert prompt registry projection, stale answer rejection,
  session-level Stop without a turn id, and explicit recovery-required state.
