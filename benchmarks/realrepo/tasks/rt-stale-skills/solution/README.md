# Reasonix kernel snapshot

A trimmed slice of the Reasonix Go kernel: the contracts a benchmark report is
priced and labelled with, a few leaf utilities, and `benchctl`, a small command
that prints what those contracts answer.

```bash
make check                                   # gofmt, vet, test
go run ./cmd/benchctl rate -model deepseek-flash -currency USD
go run ./cmd/benchctl cost -provider mimo -model mimo-v2.5 -miss 1000000
go run ./cmd/benchctl tariff -currency CNY -at 2026-10-12T02:00:00Z
go run ./cmd/benchctl arm -ablate planner,evidence
```

## Packages

| Package | Owns |
| --- | --- |
| `internal/contract/pricing` | the official price book, cost quotes, the spend ledger |
| `internal/contract/ablation` | which subsystems a benchmark arm switches off, and the arm's name |
| `internal/contract/agentgraph` | the vocabulary of one run's execution graph |
| `internal/base/retrieval` | BM25 ranking over CJK-aware tokens |
| `internal/base/rulelist` | splitting a one-line permission rule list |
| `internal/base/tokencount` | a cheap token estimate |
| `internal/base/neterr` | classifying transport errors |
| `internal/base/nilutil` | typed-nil detection |
| `cmd/benchctl` | prints rates, costs, peak schedules and arm names |

## Pricing

A `RateCard` is a price per million tokens in one currency:

| Field | Charges |
| --- | --- |
| `CacheHit` | prompt tokens served from the provider's cache |
| `Input` | prompt tokens that missed the cache, including tokens written to it |
| `Output` | completion tokens |

`OriginalCostAmount` turns a card and a `UsageTokens` breakdown into a
fixed-point `Amount`; `PricingFingerprint` keys a card in the ledger, so a
stored ledger entry stays attached to the card it was priced with.
`CurrentRate` and `SupersededRates` answer from `officialRates`, which only
ever grows: a price change appends a generation.

## Ablation

`ablation.Parse` reads a comma-separated list of modules to switch off, and
`Set.Arm` names the result (`full` for the control). The switchable modules,
in the order arm names use:

| Module | Off means |
| --- | --- |
| `evidence` | no evidence gate on completion claims |
| `planner` | no two-model planning turn |
| `subagent` | no delegation to workers |
| `retrieval` | no retrieval over the folded transcript |
| `compaction` | no context folding |
| `upstream` | a dependency edge orders its endpoints without delivering the answer |
| `recall-search` | recall can read a given address but cannot search for one |

The fold-index scale is a separate axis (`-fold-index`), not a module.

## Skills

Project skills live under `.reasonix/skills/<name>/SKILL.md`.

| Skill | Use it to |
| --- | --- |
| `price-change` | record a vendor's price change in the price book |
| `ablation-arm` | add a switchable subsystem to the benchmark's ablation arms |
| `benchctl` | check a rate, cost, peak schedule or arm name from the command line |
