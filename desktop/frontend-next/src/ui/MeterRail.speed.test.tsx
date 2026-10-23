// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import "./testkit";
import { MeterRail } from "./MeterRail";

afterEach(cleanup);

function rail(firstToken: number) {
  return render(
    <MeterRail
      tps={0} trail={[]} running={false} speed={{ output: 80, modelSeconds: 2, average: 40, firstToken }}
      metrics={{ hit: 0, miss: 0 }} ctx={null} mcp={[]} compact={{ phase: "idle" } as never} cost={[]} wallet={{} as never}
      hideAmounts={false} tasks={[]} jobs={[]} onSettings={vi.fn()} onCancelJob={vi.fn(async () => {})}
    />,
  );
}

describe("the generation speed detail", () => {
  it("names the time to first token next to the other timings", () => {
    rail(1.25);
    const row = screen.getByText("首字时间").closest("div")!;
    expect(row.textContent).toContain("1.25s");
  });

  it("shows a dash rather than a zero when no round measured one", () => {
    rail(0);
    expect(screen.getByText("首字时间").closest("div")!.textContent).toContain("—");
  });
});

describe("two rails on one screen", () => {
  it("point their speed buttons at their own detail, not one shared id", () => {
    const { container } = render(
      <>
        <MeterRail
          tps={0} trail={[]} running={false} speed={{ output: 0, modelSeconds: 0, average: 0, firstToken: 0 }}
          metrics={{ hit: 0, miss: 0 }} ctx={null} mcp={[]} compact={{ phase: "idle" } as never} cost={[]} wallet={{} as never}
          hideAmounts={false} tasks={[]} jobs={[]} onSettings={vi.fn()} onCancelJob={vi.fn(async () => {})}
        />
        <MeterRail
          tps={0} trail={[]} running={false} speed={{ output: 0, modelSeconds: 0, average: 0, firstToken: 0 }}
          metrics={{ hit: 0, miss: 0 }} ctx={null} mcp={[]} compact={{ phase: "idle" } as never} cost={[]} wallet={{} as never}
          hideAmounts={false} tasks={[]} jobs={[]} onSettings={vi.fn()} onCancelJob={vi.fn(async () => {})}
        />
      </>,
    );
    const ids = [...container.querySelectorAll(".studio-speed-detail")].map((d) => d.id);
    expect(ids).toHaveLength(2);
    expect(new Set(ids).size).toBe(2);
    for (const btn of container.querySelectorAll(".studio-meter-speed")) {
      const target = btn.getAttribute("aria-describedby")!;
      expect(container.querySelectorAll(`[id="${target}"]`)).toHaveLength(1);
    }
  });
});
