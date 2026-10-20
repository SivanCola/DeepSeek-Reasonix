# Reasonix project memory

This checkout is a trimmed snapshot of the Reasonix kernel: the billing,
ablation and graph contracts and a handful of leaf utilities, plus one small
command that drives them. The conventions below are the project's own.

## Conventions

- Go kernel under `internal/`; each package owns one concern. A package's long
  explanation belongs in its `doc.go`, not spread across implementation files.
- Judgements read structure, never wording: types, contracts, schemas. An
  error a caller must tell apart carries an identity — a sentinel, a typed
  code — never a sentence.
- A metric is its definition, not its name. Before a number becomes a finding,
  state what it counts, from which anchor, over which population, and what it
  excludes.

## Comments

Default is none — the code is the truth. Write one only when the **why** is
non-obvious: a hidden constraint, an invariant the type system cannot express,
or an external-protocol quirk. A comment states the constraint that holds now;
it does not narrate how the code got here. English, always.

- Declaration doc: ≤5 lines. Package comment: ≤8 lines, or ≤40 in a `doc.go`.
- Every other comment: ≤3 lines. Struct-field and trailing `//`: 1 line.
- One responsibility per file; 800 lines is the ceiling.

## Vendor rates

Official list prices live in one place: `officialRates` in
`internal/contract/pricing/rates.go`, oldest generation first, the last being
what a model charges today. A price change is an **append, never an edit** —
the superseded rate is what lets an installed config be recognised as holding
one of ours and brought forward. Callers read these through
`pricing.CurrentRate` and `pricing.SupersededRates` and restate none of them.

`make pricecheck` compares that table against each vendor's own page. It needs
the network, so it is not part of `make check`.

## Before every commit

```bash
make check    # gofmt -l, go vet ./..., go test ./...
```

## Change discipline

List callers, consumers of the same state and sibling features before editing.
Fix at the owning layer; a fix that breaks a neighbouring behaviour is a failed
fix. Write tests first; they must fail without the change.
