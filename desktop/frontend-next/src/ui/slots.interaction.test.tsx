// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import "./testkit";
import { SlottedView } from "./SlottedView";
import { arrange, headerBody, placement } from "./slots";
import type { ExtensionSurface, ExtensionViewNode } from "../port/wire";

afterEach(cleanup);

const view = (pluginId: string, surfaceId: string, slot?: string, body: ExtensionViewNode[] = [{ kind: "text", value: surfaceId }]): ExtensionSurface => ({
  pluginId, surfaceId, kind: "view", view: { slot, body },
});
const ids = (list: ExtensionSurface[]) => list.map((v) => `${v.pluginId}:${v.surfaceId}`);

describe("where a view stands", () => {
  it("reads the protocol's slot names as the places this window has", () => {
    expect(placement(view("p", "a", "rail"), {})).toBe("sidebar");
    expect(placement(view("p", "a", "composer.above"), {})).toBe("composer-trailing");
    expect(placement(view("p", "a", "session.header"), {})).toBe("session-header");
    expect(placement(view("p", "a", "pane.tab"), {})).toBe("pane-tab");
  });

  it("keeps the names placements were stored under before the vocabulary grew", () => {
    expect(placement(view("p", "a", "sidebar"), {})).toBe("sidebar");
    expect(placement(view("p", "a", "composer-trailing"), {})).toBe("composer-trailing");
    expect(placement(view("p", "a", "above_prompt"), {})).toBe("composer-trailing");
    expect(placement(view("p", "a", "pane.tab"), { "p:a": "composer-trailing" })).toBe("composer-trailing");
  });

  it("lets the user overrule the extension, and falls back to what it asked when a stored place is gone", () => {
    expect(placement(view("p", "a", "pane.tab"), { "p:a": "session-header" })).toBe("session-header");
    expect(placement(view("p", "a", "pane.tab"), { "p:a": "settings.section" })).toBe("pane-tab");
    expect(placement(view("p", "a", "home.card"), {})).toBe("sidebar");
    expect(placement(view("p", "a"), {})).toBe("sidebar");
  });
});

describe("arranging every view of a pane", () => {
  it("orders a place by plugin then surface, never by when a view arrived", () => {
    const late = [view("zeta", "b", "rail"), view("alpha", "z", "rail"), view("alpha", "a", "rail"), view("a-b", "x", "rail")];
    expect(ids(arrange(late, {}).sidebar)).toEqual(["a-b:x", "alpha:a", "alpha:z", "zeta:b"]);
    expect(ids(arrange([...late].reverse(), {}).sidebar)).toEqual(["a-b:x", "alpha:a", "alpha:z", "zeta:b"]);
  });

  it("sends what a full place cannot hold to the rail rather than nowhere", () => {
    const asks = ["a", "b", "c"].map((s) => view("p", s, "composer.above"));
    const placed = arrange(asks, {});
    expect(ids(placed["composer-trailing"])).toEqual(["p:a", "p:b"]);
    expect(ids(placed.sidebar)).toEqual(["p:c"]);
    const header = arrange(["a", "b", "c", "d"].map((s) => view("p", s, "session.header")), {});
    expect(ids(header["session-header"])).toEqual(["p:a", "p:b", "p:c"]);
    expect(ids(header.sidebar)).toEqual(["p:d"]);
    const tabs = arrange(["a", "b", "c", "d", "e"].map((s) => view("p", s, "pane.tab")), {});
    expect(ids(tabs["pane-tab"])).toEqual(["p:a", "p:b", "p:c", "p:d"]);
    expect(ids(tabs.sidebar)).toEqual(["p:e"]);
  });

  it("gives every place a list even when nothing stands in it", () => {
    expect(arrange([], {})).toEqual({ sidebar: [], "composer-trailing": [], "session-header": [], "pane-tab": [] });
  });
});

describe("a view in the session header", () => {
  it("keeps text, pips, meters and buttons, and lifts them out of rows and stacks", () => {
    const body: ExtensionViewNode[] = [
      { kind: "row", children: [{ kind: "pip", tone: "ok" }, { kind: "text", value: "62%" }] },
      { kind: "stack", children: [{ kind: "meter", progress: 0.5 }, { kind: "button", label: "Sync", actionId: "sync" }] },
    ];
    expect(headerBody(body)).toEqual([
      { kind: "pip", tone: "ok" },
      { kind: "text", value: "62%" },
      { kind: "meter", progress: 0.5 },
      { kind: "button", label: "Sync", actionId: "sync" },
    ]);
  });

  it("says what a node the header cannot draw holds instead of dropping it", () => {
    expect(headerBody([
      { kind: "markdown", value: "**done**", tone: "ok" },
      { kind: "kv", key: "quota", value: "620/1000" },
      { kind: "divider" },
    ])).toEqual([
      { kind: "text", value: "**done**", tone: "ok" },
      { kind: "text", value: "quota: 620/1000" },
    ]);
  });
});

describe("moving a view", () => {
  it("offers every place this window has, under its own name, and says which one holds it", () => {
    render(<SlottedView ext={view("p", "a", "pane.tab")} onAction={() => {}} onMove={() => {}} />);
    const items = screen.getAllByRole("menuitemradio", { hidden: true });
    expect(items.map((b) => b.textContent)).toEqual(["扩展页签", "输入框上方", "会话标题栏", "单独的页签"]);
    expect(items.map((b) => b.getAttribute("aria-checked"))).toEqual(["false", "false", "false", "true"]);
  });

  it("moves to the place picked, and hands the choice back without hiding the view", () => {
    const onMove = vi.fn();
    render(<SlottedView ext={view("p", "a")} onAction={() => {}} onMove={onMove} />);
    fireEvent.click(screen.getByRole("button", { name: "移动 p 的界面" }));
    fireEvent.click(screen.getByRole("menuitemradio", { name: "会话标题栏" }));
    fireEvent.click(screen.getByRole("button", { name: "移动 p 的界面" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "交还给插件" }));
    expect(onMove.mock.calls).toEqual([["session-header"], [""]]);
  });

  it("draws the body it is handed, so a narrow place can restrict what it shows", async () => {
    const ext = view("p", "a", "session.header", [{ kind: "kv", key: "k", value: "v" }]);
    render(<SlottedView ext={ext} body={headerBody(ext.view!.body)} onAction={() => {}} onMove={() => {}} />);
    expect(await screen.findByText("k: v")).toBeTruthy();
  });
});
