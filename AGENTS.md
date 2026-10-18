# Local resource ownership

Standard local `make` build/test targets and the main `pnpm` build, typecheck,
test and full-suite scripts automatically join the shared CPU/memory budget.
Use those ordinary entry points. For ad-hoc heavy commands, use:

```sh
node /absolute/path/to/this/checkout/scripts/resource-run.mjs -- <command> [args...]
```

Invoke it from the command's intended working directory. Choose focused checks
first. Different chats, worktrees and Reasonix homes share one FIFO budget;
do not launch another full suite directly to bypass a queued check. Routine
file reads and static inspection do not need the wrapper. CI already has
explicit job/shard concurrency and may invoke its checks directly.

Use `node scripts/resource-run.mjs --status` to inspect reservations and the
shared policy path. Configure budgets with `--configure --cpu-budget N
--memory-budget-mib N --cpu N --memory-mib N`; the last two set default task
reservations. Per-command `--cpu` and `--memory-mib` override those defaults.
These are cooperative reservations, not hard CPU/RSS limits. Do not bypass an
older queued task to fill spare capacity. See `docs/RESOURCE_SCHEDULING.md`.

For the filesystem-heavy full Desktop Go suite, use
`go test -count=1 -timeout=25m ./...` from `desktop/` through the wrapper.
This executes every test while retaining the build cache; it avoids enormous
test-input logs and their expensive result-cache hashing after tests finish.

Native process-exit regressions must run without a debugger attached. Use
`pnpm --dir desktop/electron test:process-lifecycle` after building the service
and Electron frontend; see `docs/RESOURCE_SCHEDULING.md`.
