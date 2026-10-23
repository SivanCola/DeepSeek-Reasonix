// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import "./testkit";
import { barsFit, flowCeiling, flowFloor, useColumn } from "./FlowResizer";

const RAIL_MIN = 232;
const DOCK_MIN = 320;

afterEach(cleanup);

describe("the reading column's two ends", () => {
  it("holds the readable measure as the floor and the window as the ceiling", () => {
    // 1600px window, both sidebars at their floors: 1048px is left for the text,
    // which is more than the readable measure, so 760 holds the floor.
    expect(flowCeiling(1600, RAIL_MIN, DOCK_MIN)).toBe(1048);
    expect(flowFloor(1600, RAIL_MIN, DOCK_MIN)).toBe(760);

    // A wide window leaves more still; the ceiling follows it, the floor does not.
    expect(flowCeiling(2400, RAIL_MIN, DOCK_MIN)).toBe(1848);
    expect(flowFloor(2400, RAIL_MIN, DOCK_MIN)).toBe(760);

    // On a window too small to hold the readable measure, the window becomes the
    // floor rather than a promise of room the layout cannot hand over.
    expect(flowFloor(1200, RAIL_MIN, DOCK_MIN)).toBe(648);
  });

  it("never reports a floor above its ceiling, however narrow the window", () => {
    for (const room of [900, 1200, 1600, 2400, 3440]) {
      expect(flowFloor(room, RAIL_MIN, DOCK_MIN)).toBeLessThanOrEqual(flowCeiling(room, RAIL_MIN, DOCK_MIN));
    }
  });

  it("never reports a ceiling below zero", () => {
    expect(flowCeiling(400, RAIL_MIN, DOCK_MIN)).toBe(0);
    expect(flowFloor(400, RAIL_MIN, DOCK_MIN)).toBe(0);
  });
});

describe("the two sidebars making room for the column", () => {
  it("leaves them alone while there is room for both", () => {
    expect(barsFit(2400, 760, 316, 316, RAIL_MIN, DOCK_MIN)).toEqual({ rail: 316, dock: 316 });
  });

  it("pins the rail at its floor rather than growing the other past its setting", () => {
    // 1300 - 760 leaves 540, under the 552 the two floors need. The rail is
    // pinned at its own floor; the workbench keeps the 316 it was set to rather
    // than growing into the room the rail gave up.
    const { rail, dock } = barsFit(1300, 760, 316, 316, RAIL_MIN, DOCK_MIN);
    expect(rail).toBe(RAIL_MIN);
    expect(dock).toBe(316);
    expect(rail + dock).toBeLessThan(316 + 316);
  });

  it("never narrows a sidebar past its own floor, and never past its setting", () => {
    for (const room of [1200, 1300, 1600, 2000, 2400]) {
      for (const ask of [760, 1000, 1400, 1800]) {
        const { rail, dock } = barsFit(room, ask, 400, 340, RAIL_MIN, DOCK_MIN);
        expect(rail).toBeGreaterThanOrEqual(RAIL_MIN);
        expect(dock).toBeGreaterThanOrEqual(DOCK_MIN);
        expect(rail).toBeLessThanOrEqual(400);
        expect(dock).toBeLessThanOrEqual(340);
      }
    }
  });

  it("recomputes from the settings, so narrowing hands the same room back", () => {
    // Derived from the settings on every call, never from the previous result:
    // that is what makes this reversible and keeps either sidebar from creeping
    // wider than the width it was set to.
    const squeezed = barsFit(1350, 760, 316, 316, RAIL_MIN, DOCK_MIN);
    const released = barsFit(2400, 760, 316, 316, RAIL_MIN, DOCK_MIN);
    expect(squeezed.rail + squeezed.dock).toBeLessThan(632);
    expect(released).toEqual({ rail: 316, dock: 316 });
    expect(barsFit(2400, 760, 316, 316, RAIL_MIN, DOCK_MIN)).toEqual(released);
  });
});

describe("the column's remembered width", () => {
  it("starts at the floor when nothing has been dragged", () => {
    localStorage.clear();
    const { result } = renderHook(() => useColumn(316, 316, RAIL_MIN, DOCK_MIN));
    expect(result.current.dragged).toBe(0);
    expect(result.current.width).toBe(result.current.floor);
    // Untouched means untouched: no inline variable outranks the measure setting.
    expect(result.current.rail).toBe(316);
    expect(result.current.dock).toBe(316);
  });

  it("remembers what a hand set, and holds it between the two ends", () => {
    localStorage.clear();
    const { result } = renderHook(() => useColumn(316, 316, RAIL_MIN, DOCK_MIN));
    act(() => result.current.commit(5000));
    // The number is kept as it was asked for...
    expect(result.current.dragged).toBe(5000);
    expect(localStorage.getItem("rx-flow-w")).toBe("5000");
    // ...while what is drawn stays inside the range this window allows.
    expect(result.current.width).toBeGreaterThanOrEqual(result.current.floor);
    expect(result.current.width).toBeLessThanOrEqual(result.current.ceiling);
  });

  it("keeps a remembered width below the floor from shrinking the column", () => {
    localStorage.clear();
    localStorage.setItem("rx-flow-w", "100");
    const { result } = renderHook(() => useColumn(316, 316, RAIL_MIN, DOCK_MIN));
    expect(result.current.width).toBe(result.current.floor);
    // Asked for, still remembered — the clamp is about what is drawn.
    expect(result.current.dragged).toBe(100);
  });
});
