# Session ownership, rewind, and worktree fallback

How Reasonix decides who may write a session, how conflicts are saved, and
how rewind and workspace isolation interact.

## Session writers

One session file has one cross-process writer at a time. The ticket is the
session lease (`.lease.lock`). Production controllers bind a generation-bound
`SessionWriter`; a rebind invalidates every older generation.

Saves that already hold a `SessionWriter` do not take the legacy `.jsonl.lock`.
Unbound test/import paths still use that compatibility flock.

The event log (`.events.jsonl`) is the source of truth. Writer-bound saves CAS
against the log tail (size + index revision/digest) and a paired in-memory
transcript view. `.jsonl` remains a compatibility projection.

## Conflicts

1. Event-log tail still matches this writer → normal save (no-op / append / replace).
2. Disk already covers the local prefix → adopt disk, no branch.
3. True divergence, replaced log, or deleted original → one stable recovery
   file keyed by root branch ID + writer generation. Later conflicts update
   that same path. There is no recovery-on-recovery chain.

## Rewind

- **Code**: restore file before-images. Already-restored files (current ==
  before) are skipped. External changes refuse overwrite.
- **Conversation**: fork a new session. The parent transcript is never
  truncated.
- **Both**: fork first, then restore files. A file conflict keeps the new
  branch and reports `partial=true`.

New checkpoints write `turns/<turn>/meta.json` plus raw `files/NNNN.before`
payloads (schema v3). v1/v2 `turn-N.json` files remain readable.

Structured writers (`write_file`, `edit_file`, `multi_edit`, notebook edit)
re-check existence, SHA-256, and mode before publish. A mismatch returns
`ErrFileChanged`.

## Worktree fallback

Delivery worktrees stay optional. Non-isolated directories use the workspace
lease (`filelock`). Conflict cards can recommend an existing worktree. Git is
never required; Windows without Git still serializes writers through the
workspace lease.
