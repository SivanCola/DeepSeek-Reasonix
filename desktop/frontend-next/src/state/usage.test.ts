import { describe, expect, it } from "vitest";
import { foldCoverage, foldUsage, quoteCoverage } from "./usage";
import { initialState } from "./session";
import type { CostCoverage, CostQuote, Usage } from "../port/wire";
import type { Metrics } from "./session_types";
import { money, moneyList } from "../i18n/format";

const ALL: CostCoverage[] = ["none", "complete", "partial", "incomplete"];

const quote = (over: Partial<CostQuote> = {}): CostQuote => ({
  original: { amount: "0.0021", currency: "USD" },
  estimated: false,
  costComplete: true,
  coverage: "complete",
  ...over,
});

const round = (over: Partial<Usage> = {}): Usage => ({
  promptTokens: 100,
  completionTokens: 20,
  totalTokens: 120,
  cacheHitTokens: 0,
  cacheMissTokens: 0,
  sessionCacheHitTokens: 0,
  sessionCacheMissTokens: 0,
  costQuote: quote(),
  ...over,
});

const fold = (...rounds: Usage[]): Metrics => rounds.reduce(foldUsage, initialState.metrics);

describe("cost coverage folds as a fact, not as an arrival order", () => {
  it("answers the pairs the aggregate is defined by", () => {
    expect(foldCoverage("none", "none")).toBe("none");
    expect(foldCoverage("complete", "none")).toBe("complete");
    expect(foldCoverage("complete", "complete")).toBe("complete");
    expect(foldCoverage("complete", "incomplete")).toBe("partial");
    expect(foldCoverage("incomplete", "complete")).toBe("partial");
    expect(foldCoverage("incomplete", "incomplete")).toBe("incomplete");
    expect(foldCoverage("partial", "complete")).toBe("partial");
    expect(foldCoverage("partial", "incomplete")).toBe("partial");
  });

  // Sixty-four triples is the whole space, so it is checked rather than sampled.
  it("cannot be moved by the order the rounds arrived in", () => {
    for (const a of ALL) {
      for (const b of ALL) {
        expect(foldCoverage(a, b)).toBe(foldCoverage(b, a));
        expect(foldCoverage(a, a)).toBe(a);
        for (const c of ALL) {
          expect(foldCoverage(foldCoverage(a, b), c)).toBe(foldCoverage(a, foldCoverage(b, c)));
        }
      }
    }
  });

  // The failure a boolean invites: the last round deciding for the ones before.
  it("never lets a complete run survive a later unpriced round", () => {
    for (const a of ALL) {
      expect(foldCoverage(foldCoverage(a, "complete"), "incomplete")).not.toBe("complete");
    }
    expect(fold(round(), round(), round({ costQuote: quote({ coverage: "incomplete" }) })).coverage).toBe("partial");
  });
});

describe("what a round contributes", () => {
  it("takes the kernel's name for it", () => {
    expect(quoteCoverage(quote({ coverage: "incomplete" }))).toBe("incomplete");
  });

  // One round is never partial and never none, so the old boolean is the whole
  // answer for a kernel that predates the field — a remote workspace can be one.
  it("reads a kernel that predates the field off its boolean", () => {
    expect(quoteCoverage(quote({ coverage: undefined }))).toBe("complete");
    expect(quoteCoverage(quote({ coverage: undefined, costComplete: false }))).toBe("incomplete");
  });

  it("counts a round that carried no quote at all as unpriced", () => {
    expect(quoteCoverage(undefined)).toBe("incomplete");
    expect(fold(round({ costQuote: undefined, cost: 0.5 })).coverage).toBe("incomplete");
  });
});

describe("a session's coverage", () => {
  it("starts at none, which is not a claim that pricing failed", () => {
    expect(initialState.metrics.coverage).toBe("none");
    expect(initialState.metrics.cost).toEqual([]);
  });

  // Both sessions total zero. Only one of them has been billed for anything,
  // and the amount is not what tells them apart.
  it("tells a billed zero from a session that has not spent", () => {
    const billedZero = fold(round({ costQuote: quote({ original: { amount: "0", currency: "USD" } }) }));
    expect(billedZero.cost).toEqual([{ currency: "USD", amount: 0 }]);
    expect(billedZero.coverage).toBe("complete");
    expect(initialState.metrics.cost).toEqual([]);
    expect(initialState.metrics.coverage).toBe("none");
    expect(billedZero.coverage).not.toBe(initialState.metrics.coverage);
  });

  it("reaches partial from either direction", () => {
    const unpriced = round({ costQuote: quote({ coverage: "incomplete", costComplete: false }) });
    expect(fold(round(), unpriced).coverage).toBe("partial");
    expect(fold(unpriced, round()).coverage).toBe("partial");
  });

  it("stays incomplete while nothing has priced", () => {
    const unpriced = round({ costQuote: quote({ coverage: "incomplete", costComplete: false }) });
    expect(fold(unpriced, unpriced).coverage).toBe("incomplete");
  });
});

describe("the reason a session fell short", () => {
  it("is kept by later rounds that priced cleanly", () => {
    const unpriced = round({ costQuote: quote({ coverage: "incomplete", costComplete: false, incompleteReason: "no_price" }) });
    const m = fold(unpriced, round(), round());
    expect(m.coverage).toBe("partial");
    expect(m.incompleteReason).toBe("no_price");
  });

  it("is replaced by the most recent round that fell short", () => {
    const first = round({ costQuote: quote({ coverage: "incomplete", costComplete: false, incompleteReason: "no_price" }) });
    const later = round({ costQuote: quote({ coverage: "incomplete", costComplete: false, incompleteReason: "missing_price_or_usage" }) });
    expect(fold(first, later).incompleteReason).toBe("missing_price_or_usage");
  });

  it("never decides the state on its own", () => {
    const m = fold(round({ costQuote: quote({ incompleteReason: "display_unavailable" }) }));
    expect(m.coverage).toBe("complete");
  });
});

// Money is not a number: it is a number and the unit it was billed in. A
// session can run on a CNY key and hand one round to a USD model, and the two
// totals are not addends. The usage page already prints one line per currency;
// the pane used to add them and label the sum with whichever round came last.
describe("a session's money keeps the currency it was billed in", () => {
  // The amount the round was actually charged, with the code that names it.
  const billed = (amount: string, currency: string, over: Partial<Usage> = {}): Usage =>
    round({
      costQuote: quote({ original: { amount, currency } }),
      currencyCode: currency,
      currency,
      ...over,
    });

  const display = (m: Metrics) => moneyList(m.cost);

  it("does not add a CNY round to a USD round", () => {
    const m = fold(billed("2.00", "USD"), billed("1.00", "CNY"));
    // Before this, cost was the scalar 3 and currency was "CNY" — the last
    // round's code — so the pane printed ¥3.00 for $2.00 + ¥1.00.
    expect(m.cost).toEqual([
      { currency: "USD", amount: 2 },
      { currency: "CNY", amount: 1 },
    ]);
    expect(display(m)).toBe("US$2.00  ·  ¥1.00");
  });

  it("still prints a single-currency session exactly as before", () => {
    const m = fold(billed("1.50", "CNY"), billed("0.25", "CNY"));
    expect(m.cost).toEqual([{ currency: "CNY", amount: 1.75 }]);
    expect(display(m)).toBe(money(1.75, "CNY"));
    expect(display(m)).toBe("¥1.75");
  });

  it("still sums rounds billed in one currency", () => {
    expect(fold(billed("1.00", "USD"), billed("2.00", "USD")).cost).toEqual([
      { currency: "USD", amount: 3 },
    ]);
  });

  // bySource means "what this source spent"; it is now split by currency too,
  // because a source can run more than one model and the units can differ.
  it("splits each source's spend by currency", () => {
    const m = fold(
      billed("1.00", "CNY", { source: "executor" }),
      billed("2.00", "USD", { source: "subagent" }),
      billed("0.50", "CNY", { source: "subagent" }),
    );
    expect(m.bySource).toEqual({
      executor: [{ currency: "CNY", amount: 1 }],
      subagent: [
        { currency: "USD", amount: 2 },
        { currency: "CNY", amount: 0.5 },
      ],
    });
  });

  it("splits what the turn added the same way", () => {
    const m = fold(billed("1.00", "CNY"), billed("2.00", "USD"));
    expect(m.turn).toEqual([
      { currency: "CNY", amount: 1 },
      { currency: "USD", amount: 2 },
    ]);
  });

  // A round that carried tokens but no amount is unpriced, not a priced zero.
  // A zero bucket would read as "this currency cost nothing" while coverage is
  // the only field that can say the price fact never arrived.
  it("adds no currency bucket for a round that carried no amount", () => {
    const unpriced = round({ costQuote: undefined, cost: undefined, currencyCode: "USD", currency: "US$" });
    const m = fold(billed("1.00", "CNY"), unpriced);
    expect(m.cost).toEqual([{ currency: "CNY", amount: 1 }]);
    expect(m.bySource).toEqual({ executor: [{ currency: "CNY", amount: 1 }] });
  });

  // Number("") is 0, so a blank amount would otherwise fold in as a priced
  // zero under a currency the round never named a figure for.
  it("reads a blank amount as no amount", () => {
    const blank = round({
      costQuote: quote({ original: { amount: " ", currency: "USD" }, coverage: "incomplete", costComplete: false }),
    });
    const m = fold(blank);
    expect(m.cost).toEqual([]);
    expect(m.coverage).toBe("incomplete");
  });
});
