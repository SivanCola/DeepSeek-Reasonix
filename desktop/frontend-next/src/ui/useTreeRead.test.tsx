// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TreeWorkspace } from "../port/hub";
import { useTreeRead } from "./useTreeRead";

const ws = (root: string): TreeWorkspace => ({ root, name: root, remembered: true, sessions: [] } as unknown as TreeWorkspace);
const window = { current: () => ({ limit: 20, limits: {}, scope: "", q: "" }) } as never;

// A kernel answer the test settles by hand.
function answers() {
  const pending: ((rows: TreeWorkspace[]) => void)[] = [];
  const hub = { tree: vi.fn(() => new Promise<TreeWorkspace[]>((resolve) => pending.push(resolve))) };
  return { hub: hub as never, calls: hub.tree, settle: (n: number, rows: TreeWorkspace[]) => pending[n](rows) };
}

describe("reading the machine's tree", () => {
  it("is unread until the kernel answers, then holds its answer", async () => {
    const k = answers();
    const { result } = renderHook(() => useTreeRead(k.hub, window));
    void result.current.reloadTree();
    expect(result.current.treeRead).toBe(false);
    await act(async () => k.settle(0, [ws("/a")]));
    expect(result.current.treeRead).toBe(true);
    expect(result.current.tree.map((w) => w.root)).toEqual(["/a"]);
  });

  it("starts a second request on retry even though the first never answered", async () => {
    const k = answers();
    const { result } = renderHook(() => useTreeRead(k.hub, window));
    void result.current.reloadTree();
    expect(k.calls).toHaveBeenCalledTimes(1);
    void result.current.retryTree();
    expect(k.calls).toHaveBeenCalledTimes(2);
    expect(k.calls).toHaveBeenNthCalledWith(2, expect.objectContaining({ retryMetadata: true }));
    await act(async () => k.settle(1, [ws("/new")]));
    expect(result.current.treeRead).toBe(true);
    expect(result.current.tree.map((w) => w.root)).toEqual(["/new"]);
    void result.current.reloadTree();
    expect(k.calls).toHaveBeenNthCalledWith(3, expect.not.objectContaining({ retryMetadata: true }));
  });

  it("drops the old request's late answer instead of overwriting the new one", async () => {
    const k = answers();
    const { result } = renderHook(() => useTreeRead(k.hub, window));
    void result.current.reloadTree();
    void result.current.retryTree();
    await act(async () => k.settle(1, [ws("/new")]));
    await act(async () => k.settle(0, [ws("/stale")]));
    expect(result.current.tree.map((w) => w.root)).toEqual(["/new"]);
  });

  it("does not queue a later reload behind a request that never answered once retried", async () => {
    const k = answers();
    const { result } = renderHook(() => useTreeRead(k.hub, window));
    void result.current.reloadTree();
    void result.current.retryTree();
    await act(async () => k.settle(1, [ws("/a")]));
    void result.current.reloadTree();
    expect(k.calls).toHaveBeenCalledTimes(3);
    await act(async () => k.settle(2, [ws("/a"), ws("/b")]));
    expect(result.current.tree.map((w) => w.root)).toEqual(["/a", "/b"]);
  });

  it("still coalesces ordinary overlapping reloads into one request in flight", async () => {
    const k = answers();
    const { result } = renderHook(() => useTreeRead(k.hub, window));
    void result.current.reloadTree();
    void result.current.reloadTree();
    void result.current.reloadTree();
    expect(k.calls).toHaveBeenCalledTimes(1);
    await act(async () => k.settle(0, [ws("/a")]));
    expect(k.calls).toHaveBeenCalledTimes(2);
  });

  it("settles as read when the kernel refuses, so the sidebar stops saying it is reading", async () => {
    const hub = { tree: vi.fn(() => Promise.reject(new Error("refused"))) } as never;
    const { result } = renderHook(() => useTreeRead(hub, window));
    await act(async () => { await result.current.reloadTree(); });
    expect(result.current.treeRead).toBe(true);
    expect(result.current.tree).toEqual([]);
  });
});
