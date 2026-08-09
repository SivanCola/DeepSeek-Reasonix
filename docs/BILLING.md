# Billing, display currency, and cost quotes

<a href="../README.md">README</a>
&nbsp;·&nbsp;
<a href="./BILLING.zh-CN.md">简体中文</a>
&nbsp;·&nbsp;
<a href="./CLI.md">CLI</a>

Reasonix separates three concepts that must never be mixed:

1. **Provider list prices** (`billing_currency` + rate card) — the original billable currency.
2. **Occurrence-time valuations** (`CostQuote.valuations`) — CNY/USD views computed when the call finished.
3. **Wallet balances** — multi-currency credit from the provider API, converted with FX only.

## Configuration

```toml
[billing]
display_currency = "auto"   # auto | CNY | USD — display only

[[providers]]
name = "deepseek-flash"
billing_currency = "USD"    # frozen list-price currency
billing_mode = "payg"       # payg | subscription_equivalent
# price / prices: custom rows always win over the official catalog
```

- Legacy `[desktop].currency` still loads and migrates into `[billing].display_currency`.
- Switching display currency **never** rewrites official or custom list prices.
- `auto` resolves in Go: config language → host region → USD. Browser locale does not change Serve/CLI/Desktop list prices.

## CostQuote wire field

Host surfaces (Desktop, Serve SSE, CLI JSON, ACP status, Remote) carry
`usage.costQuote` on usage events. Shape:

| Field | Meaning |
| --- | --- |
| `original` | Money in the provider billing currency |
| `valuations.CNY` / `valuations.USD` | Occurrence-time views |
| `valuations.*.basis` | `identity` \| `official_table` \| `fx` |
| `selected` | View for the current display preference |
| `estimated` | Always true for public rates / ECB (information-only FX) |
| `complete` | False when a shared display total cannot be formed |
| `billingMode` | `payg` or `subscription_equivalent` (PAYG-equivalent estimate) |

Legacy aliases `cost` / `costUsd` / `total_cost` / `sessionCostUsd` mirror
`selected` only and must not be treated as USD unless `currency`/`currencyCode`
says so.

Valuation priority for the non-original display currency:

1. **official_table** — same model, peer-region public list price (DeepSeek, LongCat)
2. **fx** — ECB euro reference cross rates (daily cache; never blocks model calls)
3. incomplete — keep original; never invent a unified total

## Diagnostics

```sh
reasonix doctor billing
reasonix doctor billing --json
```

Prints display preference, resolved display currency, FX cache path/freshness,
per-provider `billing_currency` / fingerprint / official catalog match, and
whether custom prices are protected.

## Wallets

DeepSeek-style balance APIs return multiple `balance_infos` without a “billing
region”. Reasonix never infers region from balance magnitude. Status lines show
`≈` converted totals when FX is available, with tooltips listing each original
wallet, conversion, rate date, and stale state.

## Related issues

This model supersedes partial Desktop-only currency patches such as PR #7790
and addresses mixed-currency ledger correctness for #4565, #3527, and #4546.
