import { useCallback, useMemo, useRef, useState } from "react";
import type { HubPort, TreeWorkspace } from "../port/hub";
import { singleFlight } from "./singleflight";
import type { TreeView, useTreeWindow } from "./treewindow";

/** The machine's folders and sessions as the kernel last answered. Reloads
 *  overlap into one request in flight; a retry starts a fresh one that no
 *  earlier request can hold up, and an answer that arrives after a newer
 *  request began is dropped rather than written over the newer one. */
export function useTreeRead(hub: Pick<HubPort, "tree">, window: Pick<ReturnType<typeof useTreeWindow>, "current">) {
  const [tree, setTree] = useState<TreeWorkspace[]>([]);
  // An empty tree before the first read settles is an unread one, not a
  // machine with no folders; only the kernel's answer can say the latter.
  const [treeRead, setTreeRead] = useState(false);
  // The view the rows in hand answer. A different view being asked for leaves
  // them describing the last one until its own answer arrives.
  const [answered, setAnswered] = useState<TreeView | null>(null);
  const newest = useRef(0);
  const make = useCallback((retryMetadata = false) => singleFlight(async () => {
    const mine = ++newest.current;
    try {
      const asked = { ...window.current(), ...(retryMetadata ? { retryMetadata: true } : {}) };
      retryMetadata = false;
      const rows = await hub.tree(asked);
      if (mine === newest.current) {
        setTree(rows);
        setAnswered({ scope: asked.scope ?? "", q: asked.q ?? "" });
      }
    } finally {
      if (mine === newest.current) setTreeRead(true);
    }
  }), [hub, window]);
  const lane = useMemo(() => ({ run: make() }), [make]);
  const readTree = useCallback(() => lane.run(), [lane]);
  const reloadTree = useCallback(() => readTree().catch(() => {}), [readTree]);
  const retryTree = useCallback(() => {
    lane.run = make(true);
    return reloadTree();
  }, [lane, make, reloadTree]);
  return { tree, treeRead, answered, readTree, reloadTree, retryTree };
}
