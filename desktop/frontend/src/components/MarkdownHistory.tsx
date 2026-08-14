// MarkdownHistory — history (non-streaming) Markdown rendering driven by the
// parse worker (Phase E). Mounted rows: check the transcript markdown cache
// (entryId + content revision) → on miss request a worker parse → render the
// resulting HAST blocks with the same components map react-markdown uses.
// Unmounted rows never reach this component, so cold-zone rows never parse.
//
// While a parse is in flight the caller-provided fallback stays on screen
// (plain full text for a fresh history mount — never truncated — or the
// committed streaming view when a live answer just completed). A single huge
// row keeps a viewport-driven tail window: opening a session paints its newest
// blocks, and scrolling toward older content prepends another bounded chunk.

import { Fragment, memo, startTransition, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { hastBlockToJsx } from "../lib/hastJsx";
import {
  estimateHastBytes,
  markdownContentRevision,
  type MarkdownBlock,
} from "../lib/markdownPipeline";
import { getMarkdownWorkerClient } from "../lib/markdownWorkerClient";
import { getTranscriptStore } from "../lib/transcriptStore";
import { createComponents } from "./markdownComponents";
import { VirtualMarkdownSourceTable } from "./MarkdownTable";

// A history surface opens at the newest transcript content. Keep the same
// ownership inside a giant Markdown row: mount a small tail, then prepend a
// larger page only when its leading edge enters the viewport. The previous
// idle loop forced one React/layout commit per second until every block was in
// the DOM, which could keep WebView2 busy for minutes after a session switch.
const MARKDOWN_TAIL_BLOCKS = 24;
const MARKDOWN_PREPEND_BLOCKS = 96;
const MARKDOWN_SENTINEL_STYLE = { display: "block", height: 1 } as const;

function cachedBlocks(entryId: string | undefined, revision: number, text: string): MarkdownBlock[] | undefined {
  if (!entryId) return undefined;
  const cached = getTranscriptStore().getMarkdown(entryId, revision);
  // The revision is a content hash; the stored source comparison is the
  // fidelity backstop against collisions and stale writes.
  return cached && cached.source === text ? cached.blocks : undefined;
}

/** Keep a tail window whose older edge advances only on viewport demand. */
function useProgressiveBlockStart(total: number, identity: MarkdownBlock[] | undefined): [number, () => void] {
  const initialStart = Math.max(0, total - MARKDOWN_TAIL_BLOCKS);
  const [window, setWindow] = useState({ identity, start: initialStart });
  // Derive the new tail synchronously so a worker result never performs one
  // discarded full-document JSX conversion before the reset effect commits.
  const current = window.identity === identity ? window.start : initialStart;
  useEffect(() => {
    setWindow((value) => value.identity === identity ? value : { identity, start: initialStart });
  }, [identity, initialStart]);
  const loadOlder = useCallback(() => {
    setWindow((value) => {
      const start = value.identity === identity ? value.start : initialStart;
      return { identity, start: Math.max(0, start - MARKDOWN_PREPEND_BLOCKS) };
    });
  }, [identity, initialStart]);
  return [current, loadOlder];
}

function useOlderBlockSentinel(identity: MarkdownBlock[] | undefined, start: number, loadOlder: () => void) {
  const sentinelRef = useRef<HTMLSpanElement>(null);
  const armedRef = useRef(true);
  const observedIdentityRef = useRef(identity);
  useEffect(() => {
    if (observedIdentityRef.current !== identity) {
      observedIdentityRef.current = identity;
      armedRef.current = true;
    }
    const sentinel = sentinelRef.current;
    if (start <= 0 || !sentinel || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => {
      const visible = entries.some((entry) => entry.isIntersecting);
      if (!visible) {
        armedRef.current = true;
        return;
      }
      if (!armedRef.current) return;
      armedRef.current = false;
      startTransition(loadOlder);
    }, { rootMargin: "240px 0px" });
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [identity, loadOlder, start]);
  return sentinelRef;
}

export const MarkdownHistory = memo(function MarkdownHistory({
  text,
  plainStatusBlocks = false,
  entryId,
  fallback,
  onParsed,
  onError,
}: {
  text: string;
  plainStatusBlocks?: boolean;
  /** History entry id (`he:<entryId>` rows) — enables the parsed-block cache. */
  entryId?: string;
  /** What to show while the worker parses (plain text or the streaming view). */
  fallback: ReactNode;
  onParsed?: () => void;
  onError?: () => void;
}) {
  const revision = useMemo(() => markdownContentRevision(text), [text]);
  // Parsed state is keyed by its source text: a text change renders the
  // fallback (never stale blocks) until the new parse lands.
  const [parsed, setParsed] = useState<{ text: string; blocks: MarkdownBlock[] } | undefined>(() => {
    const cached = cachedBlocks(entryId, revision, text);
    return cached ? { text, blocks: cached } : undefined;
  });
  const blocks = parsed && parsed.text === text ? parsed.blocks : undefined;

  useEffect(() => {
    const cached = cachedBlocks(entryId, revision, text);
    if (cached) {
      setParsed({ text, blocks: cached });
      onParsed?.();
      return;
    }
    const handle = getMarkdownWorkerClient().parse(text);
    let cancelled = false;
    handle.promise
      .then((result) => {
        if (cancelled || !result) return;
        if (entryId) {
          getTranscriptStore().setMarkdown(entryId, revision, {
            source: text,
            blocks: result.blocks,
            selectionText: result.selectionText,
            selectionRevision: result.selectionRevision,
            bytes: text.length * 2 + result.selectionText.length * 2 + estimateHastBytes(result.blocks),
          });
        }
        setParsed({ text, blocks: result.blocks });
        onParsed?.();
      })
      .catch(() => {
        if (!cancelled) onError?.();
      });
    return () => {
      cancelled = true;
      handle.cancel();
    };
    // onParsed/onError are stable caller callbacks; re-running per identity
    // change would re-request parses the cache already serves.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text, entryId, revision]);

  const components = useMemo(() => createComponents(plainStatusBlocks), [plainStatusBlocks]);
  const totalBlocks = blocks?.length ?? 0;
  const [visibleStart, loadOlder] = useProgressiveBlockStart(totalBlocks, blocks);
  const olderSentinelRef = useOlderBlockSentinel(blocks, visibleStart, loadOlder);

  // JSX per block depends only on the block and the components map; build it
  // lazily so viewport-window growth never re-converts settled blocks.
  const jsxCacheRef = useRef<{ blocks: MarkdownBlock[]; nodes: ReactNode[] } | null>(null);
  if (!blocks) return <>{fallback}</>;
  let cache = jsxCacheRef.current;
  if (!cache || cache.blocks !== blocks) {
    cache = { blocks, nodes: new Array<ReactNode>(blocks.length) };
    jsxCacheRef.current = cache;
  }
  return (
    <div
      className="md"
      data-markdown-blocks={blocks.length}
      data-markdown-visible-blocks={blocks.length - visibleStart}
      data-markdown-window-start={visibleStart}
    >
      {visibleStart > 0 && <span ref={olderSentinelRef} style={MARKDOWN_SENTINEL_STYLE} data-markdown-older-sentinel aria-hidden="true" />}
      {blocks.slice(visibleStart).map((block, offset) => {
        const index = visibleStart + offset;
        const cached = cache.nodes[index];
        if (cached !== undefined) return <Fragment key={block.key}>{cached}</Fragment>;
        const node = block.virtualTable
          ? <VirtualMarkdownSourceTable data={block.virtualTable} />
          : hastBlockToJsx(block, components);
        cache.nodes[index] = node;
        return <Fragment key={block.key}>{node}</Fragment>;
      })}
    </div>
  );
});

export default MarkdownHistory;
