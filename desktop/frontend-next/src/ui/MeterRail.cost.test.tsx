// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import "./testkit";
import { MeterRail } from "./MeterRail";
import type { CurrencyAmount } from "../state/session_types";

afterEach(cleanup);

function rail(cost: CurrencyAmount[], turnCost?: CurrencyAmount[]) {
  return render(
    <MeterRail
      tps={0} trail={[]} running={false} speed={{ output: 0, modelSeconds: 0, average: 0, firstToken: 0 }}
      metrics={{ hit: 0, miss: 0 }} ctx={null} mcp={[]} compact={{ phase: "idle" } as never} cost={cost} turnCost={turnCost} wallet={{} as never}
      hideAmounts={false} tasks={[]} jobs={[]} onSettings={vi.fn()} onCancelJob={vi.fn(async () => {})}
    />,
  );
}

const chips = (c: HTMLElement) => [...c.querySelectorAll(".studio-meter-cost")].map((e) => e.textContent ?? "");

describe("what the meter says the money is", () => {
  it("labels the running total as the session's, and what the last turn spent as the turn's", () => {
    const { container } = rail([{ currency: "CNY", amount: 6 }], [{ currency: "CNY", amount: 3 }]);
    const [session, turn] = chips(container);
    expect(session).toMatch(/本会话/);
    expect(session).toMatch(/6\.00/);
    expect(turn).toMatch(/本轮/);
    expect(turn).toMatch(/3\.00/);
  });

  it("draws only the session total when no turn has spent anything yet", () => {
    const { container } = rail([{ currency: "CNY", amount: 6 }], []);
    const all = chips(container);
    expect(all).toHaveLength(1);
    expect(all[0]).toMatch(/本会话/);
  });
});
