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

Exact save events update the affected path. Dirty-root coordination records survive restart. Ordinary failures back off for 1, 5, and 30 seconds; inaccessible roots keep a failure state without hiding their history in bulk.

The default automatically sorted, ungrouped legacy list reads bounded pages from an immutable catalog database view instead of collecting every page first. Sessions sharing a topic are paged individually. Releasing a cursor closes its dedicated read-only database connection. Activity updates appear after refreshing the snapshot.

Legacy JSONL checkpoints, trusted `.displayidx` files, and DAGs can build SQLite locator projections in the cache. Bodies are still read through their native format. DAG projection applies the selected branch and overlays and does not create a current-format manifest. Read bindings share preparation for a source and branch; releasing the last reference cancels preparation and closes its database. Native directory sessions can be read before a controller is created.

Historical root services share a 256 MiB idle-runtime budget and the existing 60-second idle TTL. Executing, approval-waiting, and bound runtimes are not idle eviction candidates. This is not a total application memory limit.

Maintenance diagnostics contain counters and resource usage, not transcript bodies. `historyMaintenance.instrumentedReadBytes` counts actual controlled-reader bytes, not stat sizes presented as I/O measurements.

## Registry reads and ownership resolution

Each successful registry verification publishes an immutable snapshot with session ownership, lifecycle, shared-topic, and source-branch lookup indexes. Navigation, execution ownership checks, and legacy adoption lookups use explicit identities. A single-session lookup does not copy workspace membership or operation journals. A successful durable mutation immediately publishes an independently owned snapshot; retained earlier snapshots do not change.

Display readers may retain a published snapshot, but it does not authorize execution. Execution and management admission still verify actual registry bytes, including older writers that preserve timestamps or generation. Overlapping verifications share a read, and canceling one caller does not cancel the others. Corrupt files, future formats, and ownership conflicts neither replace the last display snapshot nor authorize overwriting the authoritative file with cached state.

Stable snapshot lookup is O(1). Initial reads, external validation, and JSON registry writes remain O(B), where B is the registry byte count. Lookup benchmarks covering 100, 10,000, and 100,000 sessions distributed over 1, 100, and 1000 projects measure only snapshot queries, not startup acceptance.

Mainline formal session creation and input recovery remain intact. Durable operation receipts establish successful creation; a later history or sidebar projection failure cannot authorize creating another session. Runtime updates retain the existing epoch/revision owner, and bodies retain their negotiated read binding.

## Remaining acceptance boundaries

The implementation must not yet be described as complete performance governance. Custom ordering, groups, and multi-head sidebars still use the original full snapshot adapter. Current-format member metadata and historical directory discovery still need further integration into the persistent incremental projection. Legacy preparation checkpoints across restarts, oversized individual records, and append-stable cursors also require separate completion and validation.

Formats or remote protocols without the new read binding retain their negotiated reader. A read failure does not authorize changing the storage source. Explicit import, archive, recovery, copy, move, and complete export keep their management semantics.

Acceptance records interactive startup, catalog first page, first trusted window, and execution readiness separately. Thirty fixed-environment measurements at each of 100, 10,000, and 100,000 sessions, actual packaged startup and exit, and memory convergence during sustained navigation require independent evidence. Browser mock tests do not replace those gates.
