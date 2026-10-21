Add opt-in collaboration modes with short write transactions and retained isolated results.

- `auto`, `shared` and `isolated` coordinate individual writes, reject stale views and expose recovery controls. Refs #14025.
- Configured Linux verification runtimes can validate and automatically integrate an exact candidate snapshot. Refs #14025.
- Existing configurations retain their saved semantics; new-install automatic defaults remain gated by qualification. Refs #14025.
- Local controlled verification on macOS/Windows and independent nested worktrees remain unavailable. Refs #14025.
