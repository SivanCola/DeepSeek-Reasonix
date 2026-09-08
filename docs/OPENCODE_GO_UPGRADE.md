# OpenCode Go configuration upgrade (schema 10)

[中文说明](OPENCODE_GO_UPGRADE.zh-CN.md)

This upgrade fixes the local `UNSUPPORTED_REASONING_EFFORT` rejection in 1.38.2
when an OpenCode Go DeepSeek connection used Anthropic Messages,
`thinking = "enabled"`, and saved `max`. The old binary capability inference
could reject either the execution model or its independently configured planner
before sending a request.

## Automatic changes

User-global versions 7, 8, and 9 advance to 10 in one startup. Known OpenCode Go
models are grouped by recommended API: DeepSeek V4 Flash, Pro, and Flash Vision
Experimental use Chat Completions; other models use the pinned
`github.com/sky-valley/pi` v0.84.20 catalog and verified compatibility additions.
For example, Qwen3.8 Max recommends Chat and MiniMax M3 recommends Anthropic.
Unknown IDs stay on their original connection; names are not prefix-matched.

The original connection keeps its original API group when available, otherwise
its original default model's group. Other groups get stable sibling names such
as `go-chat`; conflicts get numeric suffixes. Reuse requires matching credential
references, transport settings, and compatible model settings. Prices, output
limits, vision overrides, model IDs, and explicit disabled settings are retained.
`low/high/max` are never downgraded. Legacy DeepSeek `enabled` becomes `high`;
Responses `none` becomes Chat `disabled`. Explicit custom declarations still
undergo adapter validation; incompatible choices produce an actionable error.

Matching uses actual request URL, API format, and exact model ID, including full
`/zen/go/v1/messages` URLs. Custom domains, queries, nonstandard paths, and custom
request bodies are preserved with a reason in the migration summary. Project
files receive runtime compatibility without repository writes. After this
one-time migration, manual choices of supported alternate interfaces persist.
All three DeepSeek adapters share depth controls; Responses uses wire value
`none` for disabled reasoning.

## History, search, and recovery

Default, planner, vision, guardian, recovery, subagent/profile, bot, and search
references are updated by purpose. Historical references resolve through a
separate journal without rewriting messages, tool results, reasoning, or usage.
Deleted or changed targets produce `MIGRATED_MODEL_UNAVAILABLE` instead of
silently using another account.

Current selections use the current connection and its current default model
before considering an old alias. Editing proxy settings, headers, or the
credential environment-variable reference does not block a new selection.
Saved sessions still validate their original connection. To use an edited
connection with an old session, explicitly select the model again, including
when its displayed name has not changed. This preserves the transcript and
stores an optional `model_identity` digest beside the saved model; subsequent
restarts validate that acknowledged connection. The digest contains no resolved
API key. Session copies and branches retain it. Older sidecars without the
field use the original migration journal; an older writer that drops the field
causes the next new-version resume to require acknowledgement again, rather
than silently adopting a changed connection.

Explicitly enabled native DeepSeek search on Anthropic/Responses is preserved
as a separate connection on its original API and credential reference. Explicit
search assignments follow it; automatic search prefers that account and reports
an unavailable saved search identity. Explicit `web_search = false` stays off.

Desktop runtime construction uses the selected configuration snapshot and checks
local role capabilities before creating session resources. The execution model
can use `disabled` while its planner uses `max`. Genuine errors identify role,
model, effective effort, source, API, and supported values while retaining the
typed cause. Failed startup ends the loading surface and offers settings/retry.
The composer displays the resolved default beside `auto`.

Protocol switching uses existing history conversion and cache identity rules.
The first request can lose some prefix-cache hits; the upgrade does not clear or
compact history, and repeated identical history retains stable serialization.
Running controllers retain their current configuration; new builds consume the
new snapshot.

## Persistence and rollback

For `config.toml`, the upgrade creates private-permission sidecars:

| File | Contents |
| --- | --- |
| `config.toml.opencode-go-v10.backup` | Original bytes for recovery. Protect sensitive values already present in the original file. |
| `config.toml.opencode-go-v10.json` | Aliases and hashed credential-reference/transport identity; no resolved API key. |

The edit lock covers reread, planning, backup, journal preparation, and atomic
config replacement. Encoding, comments, and unknown fields are preserved.
Prepared aliases activate only after a matching config commit. A commit marker
recovers an interrupted final acknowledgement; a previous committed generation
remains usable during a later interrupted upgrade. Ordinary saves finalize a
pending acknowledgement before removing unknown TOML fields.

Close Reasonix before restoring the backup to `config.toml`. Keep the journal
with its matching configuration and preserve permissions; startup can retry the
deterministic upgrade. Never attach the journal to another account's config.
Version 1.38.2 can read/save the migrated TOML; the separate journal survives that
renderer and a version-9 save followed by re-upgrade. Future schema versions are
not downgraded.

The next stable candidate must complete the [acceptance matrix](OPENCODE_GO_VALIDATION.md).
