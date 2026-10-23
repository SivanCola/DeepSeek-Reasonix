import type { CurrencyAmount, Metrics } from "./session_types";
import type { CostCoverage, CostQuote, Usage } from "../port/wire";

// What a billed round reports, read out of the quote rather than guessed.
// These live apart from the reducer because they answer about money and token
// counts, not about how a session's items fold together.

// The quote is the authoritative amount: it carries currency and an estimated
// flag, while usage.cost is only a legacy alias. A blank amount is no amount:
// Number("") is 0, and a priced zero is a different fact from "not priced".
export function quoteAmount(q?: { selected?: { amount: string }; original: { amount: string } }): number | undefined {
  const raw = q?.selected?.amount ?? q?.original.amount;
  if (raw === undefined || raw.trim() === "") return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

// The symbol a session starts under before any round names its currency. It
// labels the zero the pane draws before anything is priced, and nothing else:
// every billed round brings its own code.
export const DEFAULT_CURRENCY = "¥";

// addCurrency adds money to the bucket for its code, making the bucket on the
// first round that bills in that currency. Two codes never meet: they are not
// addends, and there is no rate here to turn one into the other.
export function addCurrency(totals: CurrencyAmount[], currency: string, amount: number): CurrencyAmount[] {
  const at = totals.findIndex((total) => total.currency === currency);
  if (at < 0) return [...totals, { currency, amount }];
  const next = totals.slice();
  next[at] = { currency, amount: next[at].amount + amount };
  return next;
}

// The second currency, when the host quoted one. Only a *converted* quote has
// two sides; a single-currency quote reports the same money twice and is not
// two rows.
// ROUNDS bounds the per-round series: a session that runs for hours must not
// grow an array in the reducer, and a trend older than the last few dozen
// rounds is not what the sparkline is being read for.
const ROUNDS = 48;

export function sampleRound(prev: number[], total: number): number[] {
  if (!total) return prev;
  const next = prev.length >= ROUNDS ? prev.slice(1) : prev.slice();
  next.push(total);
  return next;
}

export function altAmount(q?: {
  selected?: { amount: string; currency: string };
  original: { amount: string; currency: string };
}): { amount: number; currency: string } | null {
  if (!q?.selected || q.selected.currency === q.original.currency) return null;
  const amount = Number(q.original.amount);
  return Number.isFinite(amount) ? { amount, currency: q.original.currency } : null;
}

// foldCoverage joins two coverages into the coverage of both together: the join
// over complete and incomplete, with none as the identity and partial as the
// top. Order cannot reach the answer, so the same rounds billed in any sequence
// fold to the same state, and a session that has priced everything so far can
// never fold back to complete once an unpriced round joins it.
export function foldCoverage(a: CostCoverage, b: CostCoverage): CostCoverage {
  if (a === "none") return b;
  if (b === "none") return a;
  if (a === b) return a;
  return "partial";
}

// What one round contributes. The kernel names it; a kernel that predates the
// field leaves it out, and for a single round the boolean is the whole answer,
// because one round is never partial and never none. A round that arrived with
// no quote at all is a round whose price fact never came.
export function quoteCoverage(q?: CostQuote): CostCoverage {
  if (!q) return "incomplete";
  return q.coverage ?? (q.costComplete ? "complete" : "incomplete");
}

// What one round billed, and in which unit. A round that named no amount is
// unpriced for money purposes: it still contributes tokens and coverage, but a
// zero in its currency would read as a round that was priced and cost nothing.
function spentOf(u: Usage): CurrencyAmount | null {
  const amount = quoteAmount(u.costQuote) ?? u.cost;
  if (amount === undefined) return null;
  // The kernel sends both, and says the code is preferred: only it can tell
  // CN¥ from ¥ in an English window. The symbol stays as the fallback.
  const currency = u.currencyCode || u.costQuote?.original.currency || u.currency || DEFAULT_CURRENCY;
  return { currency, amount };
}

// foldUsage folds one billed round into the session's running metrics. It lives
// here, not in the reducer, because every line of it is about money, tokens and
// what the round was sampled against.
export function foldUsage(m: Metrics, u: Usage): Metrics {
  const src = u.source || "executor";
  const spent = spentOf(u);
  const d = u.cacheDiagnostics;
  const cov = quoteCoverage(u.costQuote);
  return {
    ...m,
    hit: m.hit + u.cacheHitTokens,
    miss: m.miss + u.cacheMissTokens,
    out: m.out + u.completionTokens,
    bySource: spent
      ? { ...m.bySource, [src]: addCurrency(m.bySource[src] ?? [], spent.currency, spent.amount) }
      : m.bySource,
    cost: spent ? addCurrency(m.cost, spent.currency, spent.amount) : m.cost,
    // Diagnostics describe the round that just billed, so they replace rather
    // than accumulate. A round that carried none leaves the last answer
    // standing instead of blanking the block mid-turn.
    prefixHash: d?.prefixHash || m.prefixHash,
    prefixChanged: d ? d.prefixChanged : m.prefixChanged,
    prefixReasons: d?.prefixChangeReasons ?? (d ? [] : m.prefixReasons),
    bodyChanged: d ? d.bodyChanged : m.bodyChanged,
    carriedMessages: d?.carriedMessages ?? m.carriedMessages,
    toolSchema: d?.toolSchemaTokens ?? m.toolSchema,
    estimated: u.costQuote?.estimated ?? u.estimated ?? m.estimated,
    coverage: foldCoverage(m.coverage, cov),
    // A round that priced cleanly explains nothing, so it leaves the standing
    // reason alone: coverage stays short, and why it is short has to stay too.
    incompleteReason: cov === "complete" ? m.incompleteReason : u.costQuote?.incompleteReason || m.incompleteReason,
    alt: altAmount(u.costQuote) ?? m.alt,
    turn: spent ? addCurrency(m.turn, spent.currency, spent.amount) : m.turn,
    rounds: sampleRound(m.rounds, u.totalTokens),
  };
}
