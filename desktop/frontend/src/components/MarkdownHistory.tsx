import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { hastBlockToJsx } from "../lib/hastJsx";
import { estimateHastBytes, markdownContentRevision, type MarkdownBlock, type MarkdownParseResult } from "../lib/markdownPipeline";
import { getMarkdownWorkerClient } from "../lib/markdownWorkerClient";
import { getTranscriptStore } from "../lib/transcriptStore";
import { createComponents } from "./markdownComponents";
import { useChatFileCandidateReport } from "./ChatFileLinkContext";
import { MarkdownSourceTable } from "./MarkdownTable";
import "katex/dist/katex.min.css";
import "./harness-chat/MarkdownText.css";

const Block = memo(function Block({ block, components }: { block: MarkdownBlock; components: ReturnType<typeof createComponents> }) {
  return block.virtualTable ? <MarkdownSourceTable data={block.virtualTable} /> : hastBlockToJsx(block, components);
});

/** Worker parsing is independent of viewport geometry. Every block uses natural flow. */
const MarkdownHistory = memo(function MarkdownHistory({ text, streaming = false, plainStatusBlocks = false, cacheKey, fallback, onParsed, onError }: {
  text: string; streaming?: boolean; plainStatusBlocks?: boolean; cacheKey?: string; fallback: ReactNode;
  onParsed?: () => void; onError?: () => void;
}) {
  // The revision is a cache key for settled content only: a streaming body is
  // never stored, so hashing it on every delta would be O(body) work per
  // commit for a lookup that cannot hit.
  const revision = useMemo(() => (streaming ? 0 : markdownContentRevision(text)), [streaming, text]);
  const [parsed, setParsed] = useState<{ text: string; result: MarkdownParseResult }>();
  const previous = useRef<MarkdownParseResult | undefined>(undefined);
  const root = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    if (visible || typeof IntersectionObserver === "undefined" || !root.current) return;
    const observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) { setVisible(true); observer.disconnect(); }
    }, { rootMargin: "800px" });
    observer.observe(root.current);
    return () => observer.disconnect();
  }, [visible]);
  const components = useMemo(() => createComponents(plainStatusBlocks), [plainStatusBlocks]);
  const cached = useMemo(
    () => (!streaming && cacheKey ? getTranscriptStore().getMarkdown(cacheKey, revision) : undefined),
    [cacheKey, revision, streaming],
  );
  useEffect(() => {
    if (!visible && !streaming) return;
    if (cached?.blocks) { onParsed?.(); return; }
    let cancelled = false;
    const request = getMarkdownWorkerClient().parse(text);
    void request.promise.then(result => {
      if (cancelled || !result) return;
      // Retain unchanged AST identities across stream publications and
      // finalization, so React keeps native selection and code disclosure
      // hosts. The comparison is the fingerprint the parse already computed:
      // serializing both trees here cost O(blocks x block size) of string
      // allocation on the main thread on every streamed commit.
      const stable = previous.current?.blocks;
      if (stable) result.blocks = result.blocks.map((block, index) =>
        stable[index]?.key === block.key && stable[index]?.fingerprint === block.fingerprint ? stable[index] : block);
      previous.current = result;
      setParsed({ text, result });
      if (cacheKey && !streaming) getTranscriptStore().setMarkdown(cacheKey, revision, {
        source: text, blocks: result.blocks, selectionText: result.selectionText,
        selectionRevision: result.selectionRevision,
        bytes: text.length * 2 + result.selectionText.length * 2 + estimateHastBytes(result.blocks),
      });
      onParsed?.();
    }).catch(() => { if (!cancelled) onError?.(); });
    return () => { cancelled = true; request.cancel(); };
  }, [cacheKey, cached, onError, onParsed, revision, streaming, text, visible]);
  const result = cached?.blocks ? cached : parsed && (parsed.text === text || text.startsWith(parsed.text)) ? parsed.result : undefined;
  // Only blocks the parser has already committed are reported, so a streaming
  // answer never asks the host about a half-written path.
  useChatFileCandidateReport(result?.blocks, revision);
  const nodes = useMemo(() => result?.blocks.map(block => <Block key={block.key} block={block} components={components} />), [result, components]);
  const pending = !cached?.blocks && parsed && text.startsWith(parsed.text) ? text.slice(parsed.text.length) : "";
  return <div ref={root} className="md" data-markdown-blocks={result?.blocks.length}>
    {result ? <>{nodes}{pending && <span style={{ whiteSpace: "pre-wrap" }}>{pending}</span>}</> : fallback}
  </div>;
});
export default MarkdownHistory;
