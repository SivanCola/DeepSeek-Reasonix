# History discovery, reading preparation, and execution recovery

Ordinary startup does not migrate transcript formats. Existing JSONL, DAG, and directory sessions keep their native storage; newly created sessions use the current directory format. Catalogs and history locators are disposable projections, not authoritative transcripts.

## Readiness has three meanings

- **Catalog available:** the database is open and existing metadata can be queried. Discovery may still be incomplete.
- **History readable:** a validated display window is available for the selected session. Preparation runs independently of its execution controller and can be canceled.
- **Execution ready:** the complete execution context and write authority have been restored. Reading history does not authorize sending. Draft editing and navigation remain available during recovery.

Discovery reads file identity, attributes, and bounded metadata sidecars. Unknown fields remain unknown; an unknown turn count is not zero. A failed or interrupted scan cannot declare the unvisited remainder missing. A parseable prefix of damaged authoritative content is not published as a complete history.

## Maintenance boundaries implemented

Ordinary startup no longer repairs display indexes across the history library. The catalog instance is published before discovery, watchers register before scanning, and the active session restores from its saved identity. Pending creates and durable operations remain owned by their existing recovery lifecycle.

Discovery preserves directory iterators within the process and rotates roots. Each background slice admits at most 128 entries, 4 MiB, or 50 ms, with a minimum 100 ms yield and a shared 8 MiB/s input budget. Foreground preparation and background scanning each have concurrency 1. The visible project's metadata has priority; other roots pause during foreground preparation. Controlled reads check cancellation in blocks of at most 64 KiB. These are scheduling parameters, not hard real-time or measured end-to-end guarantees.

Queued metadata discovery retains at most eight iterators. Background roots occupy at most seven slots, leaving one for the visible workspace. Waiting roots enter when a retained scan finishes; admitted scans keep their exact iterator and progress. Repeated workspace switches do not bypass the cap or evict unfinished scans. If all slots are occupied, a newly visible root waits for a slot; small exact-path updates remain independent of admission. This bound covers queued discovery, not explicit synchronous management scans.

Exact save events update the affected path. Dirty-root coordination records survive restart. Ordinary failures back off for 1, 5, and 30 seconds; inaccessible roots keep a failure state without hiding their history in bulk.

An optional legacy root that has never contained cataloged history may be absent on a fresh installation. Its discovery completes empty without creating the directory. An absent root with retained catalog rows remains unavailable; it cannot confirm those rows as missing. Creating the optional directory later schedules ordinary discovery again.

Per-workspace completeness uses that discovery result. It does not silently omit an unavailable root because a later filesystem stat reports it absent; retained rows remain visible while the failed-directory count reports incomplete discovery.

The automatically sorted legacy all-sessions view reads bounded pages from an immutable catalog database view instead of collecting every page first, including when the workspace contains groups. Time filters run in the page query with a cutoff frozen for the snapshot. Automatically sorted pinned workspace shells use the same adapter and read only pinned current-format member headers. Sessions sharing a topic are paged individually. Releasing a cursor closes its dedicated read-only database connection. Activity updates appear after refreshing the snapshot.

Automatically sorted group and ungrouped views use the same bounded result pages. Membership is fixed by the captured organization and matches physical source keys, so sharing a topic does not put two sessions in the same group. Current-format group/pin admission happens before reading member headers. Empty groups stay empty; missing groups report an error. Membership edits appear only in a new snapshot. Sparse group predicates may still scan database metadata keys; this is not a constant-time lookup guarantee or completion of incremental organization import. Source-key comparison uses the existing hash contract without resolving each candidate file or adding a persisted schema/index function.

Text filtering in automatically sorted single-head views also keeps a fixed catalog snapshot and reads metadata in bounded batches. It preserves Go Unicode case matching over the displayed title, preview, and physical source key, including localized automatic titles. Source keys come from captured catalog identities without resolving each candidate file. Only matching result pages remain resident. Sparse or absent matches can still scan all metadata; this is bounded memory, not indexed full-text search or a constant-latency guarantee. Cancellation is checked between candidates and database batches.

Legacy JSONL checkpoints, trusted `.display-index.json` files, and DAGs can build SQLite locator projections in the cache. Bodies are still read through their native format. DAG projection applies the selected branch and overlays and does not create a current-format manifest. Bound readers and compatibility paging share one preparation owner for each source, branch, and observed generation. Replacing a source cancels the old generation's reads; releasing one reader does not cancel its peers. Retiring owners remain discoverable until their database is closed, so immediate reopen and canceled successor tasks cannot bypass the cache-close barrier. Shutdown also cancels preparations borrowed without a persistent read handle. Native directory sessions can be read before a controller is created.

Historical root services share a 256 MiB idle-runtime budget and the existing 60-second idle TTL. Executing, approval-waiting, and bound runtimes are not idle eviction candidates. This is not a total application memory limit.

Bound JSONL/checkpoint and DAG readers also provide paged authored-turn outlines and direct turn/message anchors without creating an execution controller. Outline positions use a partial SQLite index; previews decode only the requested user records, one at a time, and retain bounded text. The optional answer preview is omitted on this cold path. Primary display-message identities resolve directly to fixed-cut cursors; derived subrows are not advertised as independent locator identities. Outline and anchor requests validate the same source, branch and rewrite generation as window reads. Older services without `history-native-navigation-v1` keep their existing navigation protocol. Read cancellation or source replacement cannot publish a partial outline into another navigation.

Schema-1 native event logs without a trusted display sidecar use the same reader. Replace/append records are folded into disk-backed message locations; even a replacement containing the entire conversation is decoded one message at a time. Broken append chains, future schemas, and torn tails cannot publish a valid prefix as complete history. JSON field ordering is preserved as a compatibility property. A validated cached generation avoids rescanning even a header placed after a large message array. Only derived SQLite files change; checkpoints, event logs, and compatibility display sidecars remain untouched.

Large-field refs returned by bound native windows retain their read handle. Content chunks use the same pager, source cut and cancellation lifetime; releasing or replacing the handle cannot redirect an old ref into a new reader or a management target. Reading a field no longer invokes the compatibility checkpoint-repair path for these refs.

Unbound compatibility field reads for formats accepted by the native pager also borrow that preparation owner, including explicit management targets. They validate the native source before and after reading and do not publish a compatibility display sidecar or repair a stale checkpoint. Canceling one borrowed read does not cancel a bound peer. Unsupported preparation paths and ancient event-row refs still use their existing compatibility reader; this does not give those protocols the lifetime of a persistent read handle.

Cold compatibility readers resolve the captured historical path against known source directories. A global tab's current workspace directory does not replace the original global history directory. Paging, field reads, and older preview RPCs share that resolution; unknown roots and symlinks escaping known roots remain rejected.

Maintenance diagnostics contain counters and resource usage, not transcript bodies. `historyMaintenance.instrumentedReadBytes` counts actual controlled-reader bytes, not stat sizes presented as I/O measurements.

## Registry reads and ownership resolution

Each successful registry verification publishes an immutable snapshot with session ownership, lifecycle, shared-topic, and source-branch lookup indexes. Navigation, execution ownership checks, and legacy adoption lookups use explicit identities. A single-session lookup does not copy workspace membership or operation journals. A successful durable mutation immediately publishes an independently owned snapshot; retained earlier snapshots do not change.

Source and session invalidation tokens are built once from that same publication, including source proofs, lifecycle generations, and unknown persisted fields. Workspace membership and source fences share one byte verification per page validation and query the related identities instead of cloning the registry and rescanning every mapping for each row. Title/pin edits and unrelated session changes do not revoke a retained read. Display copies borrow the exact publication's immutable token index; equal numeric generations alone cannot establish that relationship.

Display readers may retain a published snapshot, but it does not authorize execution. Execution and management admission still verify actual registry bytes, including older writers that preserve timestamps or generation. Overlapping verifications share a read, and canceling one caller does not cancel the others. Corrupt files, future formats, and ownership conflicts neither replace the last display snapshot nor authorize overwriting the authoritative file with cached state.

Stable snapshot lookup is O(1). Initial reads, external validation, and JSON registry writes remain O(B), where B is the registry byte count. Lookup benchmarks covering 100, 10,000, and 100,000 sessions distributed over 1, 100, and 1000 projects measure only snapshot queries, not startup acceptance.

Mainline formal session creation and input recovery remain intact. Durable operation receipts establish successful creation; a later history or sidebar projection failure cannot authorize creating another session. Runtime updates retain the existing epoch/revision owner, and bodies retain their negotiated read binding.

## Remaining acceptance boundaries

The implementation must not yet be described as complete performance governance. Custom ordering and multi-head sidebars still use the original full snapshot adapter. Importing old source-dependent organization preferences can still enumerate legacy pages. The ordinary all-sessions view still reads all current-format member headers; historical directory discovery still needs integration into the persistent incremental projection. A shell still collects all explicitly pinned rows. These remaining paths prevent a claim that every startup/list configuration is independent of library size.

Legacy preparation checkpoints across restarts, oversized individual messages, and append-stable cursors require further implementation and validation. Checkpoint preparation currently bounds a single decoded record at 16 MiB; it does not yet spool larger records. Event arrays are streamed, but individual provider messages are still decoded as values. Cold legacy full-text search still requires its existing compatibility path. No parseable prefix is certified as a complete replacement for unsupported or damaged history.

Formats or remote protocols without the new read binding retain their negotiated reader. A read failure does not authorize changing the storage source. Explicit import, archive, recovery, copy, move, and complete export keep their management semantics.

Acceptance records interactive startup, catalog first page, first trusted window, and execution readiness separately. Thirty fixed-environment measurements at each of 100, 10,000, and 100,000 sessions, actual packaged startup and exit, and memory convergence during sustained navigation require independent evidence. Browser mock tests do not replace those gates.
