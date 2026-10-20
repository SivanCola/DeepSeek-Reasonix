// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import type { AgentPort } from "../port/port";
import type { RuntimeView, TreeWorkspace } from "../port/hub";
import { useViewed } from "./unread";
import { sessionKey, useUnreadSince } from "../state/newAnswers";

const rt: RuntimeView = { id: "r1", base: "", root: "/w", name: "w", sessionPath: "/a" };
const book = (unread: boolean): TreeWorkspace[] => [{ root: "/w", name: "w", sessions: [{ path: "/a", name: "a", ...(unread ? { unread: true } : {}) }] }];

function mount(initial: { tree: TreeWorkspace[]; rts: RuntimeView[]; focused?: boolean }) {
  vi.spyOn(document, "hasFocus").mockReturnValue(initial.focused ?? true);
  const port = { markSessionViewed: vi.fn(() => Promise.resolve()) } as unknown as AgentPort & { markSessionViewed: ReturnType<typeof vi.fn> };
  const ports = new Map([["r1", port as AgentPort]]);
  const view = renderHook((p: { tree: TreeWorkspace[]; rts: RuntimeView[] }) => useViewed({ runtimes: p.rts, ports, active: "r1", tree: p.tree, remote: {}, reload: () => Promise.resolve() }), {
    initialProps: { tree: initial.tree, rts: initial.rts },
  });
  return { port, view };
}

beforeEach(() => {});
afterEach(() => vi.restoreAllMocks());

describe("a session that is already in front", () => {
  it("is marked seen when the window opens on it", async () => {
    const { port } = mount({ tree: book(true), rts: [rt] });
    await act(async () => {});
    expect(port.markSessionViewed).toHaveBeenCalledTimes(1);
  });

  it("is marked seen when the session list arrives after the pane did", async () => {
    const { port, view } = mount({ tree: [], rts: [rt] });
    await act(async () => {});
    expect(port.markSessionViewed).not.toHaveBeenCalled();
    view.rerender({ tree: book(true), rts: [rt] });
    await act(async () => {});
    expect(port.markSessionViewed).toHaveBeenCalledTimes(1);
  });

  it("is marked seen when the pane learns its session path after it was in front", async () => {
    const { port, view } = mount({ tree: book(true), rts: [{ ...rt, sessionPath: "" }] });
    await act(async () => {});
    expect(port.markSessionViewed).not.toHaveBeenCalled();
    view.rerender({ tree: book(true), rts: [rt] });
    await act(async () => {});
    expect(port.markSessionViewed).toHaveBeenCalledTimes(1);
  });

  it("stays unread while the window has no focus, and is marked when focus returns", async () => {
    const { port } = mount({ tree: book(true), rts: [rt], focused: false });
    await act(async () => {});
    expect(port.markSessionViewed).not.toHaveBeenCalled();
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    expect(port.markSessionViewed).toHaveBeenCalledTimes(1);
  });

  it("is not asked about again and again when the kernel refuses", async () => {
    const { port, view } = mount({ tree: book(true), rts: [rt] });
    port.markSessionViewed.mockImplementation(() => Promise.reject(new Error("refused")));
    await act(async () => {});
    for (let i = 0; i < 4; i++) {
      view.rerender({ tree: book(true), rts: [rt] });
      await act(async () => {});
    }
    expect(port.markSessionViewed.mock.calls.length).toBeLessThanOrEqual(2);
  });
});

describe("where the person last looked is kept for the new-answer line", () => {
  const row = (path: string, over: object): TreeWorkspace[] => [{ root: "/w", name: "w", sessions: [{ path, name: path, ...over }] }];

  it("is taken from the unread row the moment it is marked seen, and kept after the mark clears", async () => {
    const own = { ...rt, sessionPath: "/since-a" };
    const { view } = mount({ tree: row("/since-a", { unread: true, viewedAt: 1234 }), rts: [own] });
    const since = renderHook(() => useUnreadSince(sessionKey(undefined, "/since-a")));
    await act(async () => {});
    expect(since.result.current).toBe(1234);
    view.rerender({ tree: row("/since-a", {}), rts: [own] });
    await act(async () => {});
    expect(since.result.current).toBe(1234);
  });

  it("is not taken for a row that is not unread, or from a kernel that sends no time", async () => {
    mount({ tree: row("/since-b", { viewedAt: 99 }), rts: [{ ...rt, sessionPath: "/since-b" }] });
    await act(async () => {});
    expect(renderHook(() => useUnreadSince(sessionKey(undefined, "/since-b"))).result.current).toBeUndefined();
    mount({ tree: row("/since-c", { unread: true }), rts: [{ ...rt, sessionPath: "/since-c" }] });
    await act(async () => {});
    expect(renderHook(() => useUnreadSince(sessionKey(undefined, "/since-c"))).result.current).toBeUndefined();
  });

  it("is taken when the list reports the row unread after the pane was already in front", async () => {
    const own = { ...rt, sessionPath: "/since-d" };
    const { view } = mount({ tree: row("/since-d", {}), rts: [own] });
    await act(async () => {});
    view.rerender({ tree: row("/since-d", { unread: true, viewedAt: 55 }), rts: [own] });
    await act(async () => {});
    expect(renderHook(() => useUnreadSince(sessionKey(undefined, "/since-d"))).result.current).toBe(55);
  });
});
