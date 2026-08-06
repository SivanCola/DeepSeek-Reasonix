import { useCallback, useEffect, useRef, useState } from "react";
import { onFrame, running, send, type Frame } from "./bridge";

type Entry =
  | { id: number; role: "user"; text: string }
  | { id: number; role: "assistant"; text: string }
  | { id: number; role: "tool"; text: string }
  | { id: number; role: "notice"; text: string; level: "info" | "warn" };

type Cache = { rate: number; known: boolean };

let nextID = 0;
const id = () => ++nextID;

export default function App() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [draft, setDraft] = useState("");
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [cache, setCache] = useState<Cache>({ rate: 0, known: false });
  const streamRef = useRef<number | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  // Appends a text delta to the assistant entry the current turn is streaming
  // into, opening one on the first delta.
  const appendDelta = useCallback((text: string) => {
    setEntries((prev) => {
      const target = streamRef.current;
      if (target !== null) {
        return prev.map((e) => (e.id === target && e.role === "assistant" ? { ...e, text: e.text + text } : e));
      }
      const entry: Entry = { id: id(), role: "assistant", text };
      streamRef.current = entry.id;
      return [...prev, entry];
    });
  }, []);

  useEffect(() => {
    void running().then(setBusy);

    return onFrame((frame: Frame) => {
      switch (frame.kind) {
        case "ready":
          setReady(true);
          break;
        case "text":
          if (frame.text) appendDelta(frame.text);
          break;
        case "tool":
          setEntries((prev) => [...prev, { id: id(), role: "tool", text: frame.tool ?? "" }]);
          // A tool call ends the current text run; the next delta starts a
          // fresh assistant bubble below it.
          streamRef.current = null;
          break;
        case "notice":
          setEntries((prev) => [
            ...prev,
            { id: id(), role: "notice", text: frame.text ?? "", level: frame.level ?? "info" },
          ]);
          break;
        case "usage":
          setCache({ rate: frame.cacheHitRate, known: frame.cacheKnown });
          break;
        case "turn_done":
          streamRef.current = null;
          setBusy(false);
          if (frame.err) {
            setEntries((prev) => [...prev, { id: id(), role: "notice", text: frame.err!, level: "warn" }]);
          }
          break;
      }
    });
  }, [appendDelta]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [entries]);

  const submit = useCallback(async () => {
    const text = draft.trim();
    if (!text || busy || !ready) return;

    setEntries((prev) => [...prev, { id: id(), role: "user", text }]);
    setDraft("");
    setBusy(true);
    streamRef.current = null;
    try {
      await send(text);
    } catch (err) {
      setBusy(false);
      setEntries((prev) => [
        ...prev,
        { id: id(), role: "notice", text: String(err), level: "warn" },
      ]);
    }
  }, [draft, busy, ready]);

  const status = !ready ? "starting…" : busy ? "working…" : "ready";

  return (
    <div className="app">
      <header className="bar">
        <span className="title">Reasonix Lite</span>
        <span className="spacer" />
        <span className="cache" title="Session prompt-cache hit rate">
          cache {cache.known ? `${Math.round(cache.rate * 100)}%` : "—"}
        </span>
        <span className={`status ${busy ? "busy" : ""}`}>{status}</span>
      </header>

      <main className="transcript">
        {entries.length === 0 && <p className="empty">Ask for something to get started.</p>}
        {entries.map((entry) => (
          <div key={entry.id} className={`entry ${entry.role}${entry.role === "notice" ? ` ${entry.level}` : ""}`}>
            {entry.role === "tool" ? <code>{entry.text}</code> : entry.text}
          </div>
        ))}
        <div ref={bottomRef} />
      </main>

      <footer className="composer">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends; Shift+Enter is a newline.
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void submit();
            }
          }}
          placeholder={ready ? "Send a message…" : "Starting a session…"}
          rows={3}
          disabled={!ready}
        />
        <button onClick={() => void submit()} disabled={!ready || busy || !draft.trim()}>
          Send
        </button>
      </footer>
    </div>
  );
}
