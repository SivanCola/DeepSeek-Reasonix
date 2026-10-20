# realrepo

Long tasks over a pinned snapshot of this repository, for measuring changes
that aim to cut model round trips. The `benchmarks/e2e` tasks finish in about
5–15 requests and ten seconds; a real task in this tree took a flash-class
model 38 requests, most of them after a single read. A suite of short fixtures
cannot show that gap moving, so these tasks are shaped like the real one:
explore several packages, edit several files, verify, and recover from the
verifier.

```sh
go build -o bin/reasonix ./cmd/reasonix
go run ./cmd/e2ebench -suite benchmarks/realrepo -bin "$PWD/bin/reasonix" -model deepseek-flash \
  -budget 0 -trials 2 -trajectories traj -json realrepo.json
go run ./cmd/e2ebench -suite benchmarks/realrepo -bin "$PWD/bin/reasonix" -task rt-stale-skills
```

The agent reads its provider config from `REASONIX_HOME` (default
`~/.reasonix`); point it at a disposable one to keep benchmark sessions out of
your own.

The default `-budget` (800k tokens) can stop the suite partway through, hence
`-budget 0`. Under `-trials`, each trial keeps its own
`<task>.trial-N.trajectory.jsonl`.

## The snapshot

Every `workdir/` is the same Go module (`module reasonix`, stdlib only, no
network needed to build or test):

- **Pinned code.** `git archive` of commit `42636ddcb425` for
  `internal/contract/{pricing,ablation,agentgraph}` and
  `internal/base/{retrieval,rulelist,tokencount,neterr,nilutil}` — about 5k
  lines of real kernel code with its real tests and doc comments.
- **Fixture-authored files** in `base/`: `go.mod`, a trimmed `REASONIX.md` (the
  project's own conventions, so the agent loads standing instructions the way
  it does here), a `Makefile` with `check`, a module `README.md`, the
  `cmd/benchctl` command that consumes `pricing` and `ablation` from outside
  their package, and one project skill.
- **One pinned test anchored to the snapshot day.**
  `TestCatalogPricesHaveBeenCheckedRecently` compares each row's `CheckedOn`
  with the wall clock, which would fail every workdir 90 days after the oldest
  pinned date (2026-11-20). `base/` replaces that file with one that reads
  staleness at 2026-10-19 instead; nothing else in the pin is edited.
- **Per-task overlay** in `overlay/<task-id>/`, applied last. Only
  `rt-stale-skills` has one.

`snapshot.sh` rebuilds every workdir from those three sources and
`snapshot.sh --check` fails if a committed workdir has drifted from them. Edit
`base/` or `overlay/`, never a workdir, then rerun the script. Moving the pin is
a new suite version: results from two pins do not compare.

## Tasks

| Task | Kind | What it exercises |
| --- | --- | --- |
| `rt-cache-write-rate` | multi-file Go change, test, README | Adds a `CacheWrite` rate to `RateCard` and threads it through cost, fingerprint, card identity and the catalog, then prices Anthropic's `claude-sonnet-4-5` with it: a new `officialRates` row, documentation and checked-on constants, an endpoint host, and `benchctl rate`/`cost` output. The fingerprint has to stay byte-identical for every existing card, so the change to `PricingFingerprint` cannot be the obvious one. |
| `rt-stale-skills` | documentation only, no recognised verifier | Three project skills name paths, functions, `make` targets and `benchctl` subcommands and flags that do not exist, and one contradicts the append-only rule in `REASONIX.md`. Fixing them takes reading `rates.go`, `ablation.go`, both `benchctl` files and the `Makefile`, then editing only Markdown — the shape behind #13293, where `stale_verification` stays owed after a skill edit because no check covers it. |
| `rt-rate-lookup-signatures` | API migration with a likely compile-error loop | Changes `CurrentRate` to `(RateCard, bool)`, `LookupCatalog` to a `CatalogKey` struct, and `IsOfficialCard`, `MatchesCatalog` and `LiveTariff` to a `ModelRef`: about 20 call sites across 11 files. Most are in `_test.go` files and dereference the old pointer, so `go build ./...` can pass while `go vet` and `go test` fail. |
| `rt-plan-ablation-module` | plan-mode with `complete_step` | Five numbered steps adding a `memory` ablation module, summaries, `ModuleHelp` and `benchctl arm -list`. The prompt opens with the host's own plan-first marker (`control.PlannerRouteMarker`), which routes the turn through the planner, and asks for each step to be signed off before the next. An existing test pins the `all` arm name and has to be updated, which adds a red-test step. |

Every grader stages hidden tests and checks after the run: `gofmt -l`, `go vet
./...` and `go test ./...` for the Go tasks; for `rt-stale-skills`, every
backticked path, identifier, `make` target and `benchctl` command line in each
skill must resolve against the tree, and a SHA-256 manifest proves no Go source, `go.mod`,
`Makefile` or `REASONIX.md` changed. Each task fails its pristine seed and
passes its `solution/`, both checked by `cmd/e2ebench`'s corpus tests.

## Reading the numbers

```text
Metric:        requests per task
Anchor:        the trajectory digest's requests_by_source, summed over sources
Population:    every graded trial of a task that ran to its own stop
Excludes:      tasks the token budget skipped, and runs that hit timeout_sec
```

Requests is the headline because round trips are what these tasks exist to
move. Read it beside the solve rate and the report's round outcomes: a change
that drops requests by giving up on verification has not cut a round trip, it
has cut a check. `rt-stale-skills` is read for whether the run finishes at all
and how many rounds go to re-verifying after its last write.

## Baseline

`deepseek-flash`, 2 trials per task, at studio `0b8de9e63`. Requests are the
trajectory's executor requests; all 8 runs ran to their own stop.

| Task | Solved | Requests | `edit_file` calls | Edit rounds |
| --- | --- | --- | --- | --- |
| `rt-cache-write-rate` | 2/2 | 31, 28 | 21, 20 | 8, 8 |
| `rt-plan-ablation-module` | 2/2 | 24, 30 | 10, 13 | 9, 7 |
| `rt-rate-lookup-signatures` | 2/2 | 13, 15 | 10, 17 | 2, 6 |
| `rt-stale-skills` | 2/2* | 20, 26 | 3, 2 | 3, 3 |

\* One run first graded as failed, on a glob (`*_rates_test.go`) and a
placeholder (`benchctl <subcommand>`) that were both correct. The grader now
resolves globs against the tree and skips placeholders, and that run counts
as solved. The same task's first version failed a run for naming
`CatalogEntry.EffectiveFrom` in order to warn against it; that check went too.

Reads and edits already batch: 20 edit calls fit into 8 rounds, and several
`edit_file` calls to one file often share a round. Rounds that edit a file the
previous round also edited are rare (0–2 per run), so steering same-file edits
to `multi_edit` has little to save. On this suite's first version, a
`ToolBatchPolicy` sentence asking for it drew no `multi_edit` call and 167
requests against 158.

`rt-plan-ablation-module` spends five rounds per run on `complete_step` alone.
A description change inviting the sign-off to share the next step's reply was
taken up in 2 of 4 trials (4 of 5 sign-offs shared each time, against 0 of 20
in control), and requests did not move: 30, 25, 21, 28 against 27, 23, 24, 30.
At this spread, four trials per arm cannot show a saving of a few rounds.

## Gaps

- **Below the intended size, in places.** The tasks were designed for 25–50
  requests. After widening, the median is 25, but `rt-rate-lookup-signatures`
  still runs 13–15: the model greps every call site first and batches the edits,
  so even about 20 call sites cost few rounds, and no run hit a compile-error
  loop.
- **Lexical grader.** `rt-stale-skills` judges Markdown by resolving what it
  names. It has misjudged correct text twice (see the baseline note); a new
  failure there should be read before it is counted.
- **No git history.** The workdir is a plain directory: `git diff`, `git log`
  and `git status` have nothing to read, so runs that lean on them in this tree
  behave differently here.
- **Plan marker and `-force-planner`.** `rt-plan-ablation-module` carries the
  plan-first marker in its prompt, so it engages the planner in every arm,
  including runs without `-force-planner`. Exclude it when comparing planner
  arms.
