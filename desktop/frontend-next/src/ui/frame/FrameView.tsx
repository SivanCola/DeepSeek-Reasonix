import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { say } from "../../i18n/kernel";
import type { ExtensionSurface } from "../../port/wire";
import type { Slot } from "../slots";
import { clampHeight, decodeFrameMessage, DropCounter, FRAME_PROTOCOL, RateGate, SLOT_HEIGHTS, themeTokens } from "./bridge";

/** FrameHost is what a frame may reach through Studio: its own document's
 *  address, and its own plugin's declared actions and submit. */
export interface FrameHost {
  url(ext: ExtensionSurface): string;
  act(ext: ExtensionSurface, actionId: string, args?: Record<string, string>): void;
  submit(ext: ExtensionSurface, values: Record<string, unknown>): void;
}

// Renderer memory only: what a frame asked to keep, and which frames have
// already been on screen, so a remount restores without replaying its motion.
const saved = new Map<string, unknown>();
const shown = new Set<string>();

const theme = () => ({ scheme: document.documentElement.dataset.theme ?? "", tokens: themeTokens() });

type Phase = { kind: "checking" } | { kind: "ready" } | { kind: "failed"; why: string };

export function FrameView({ ext, slot, host }: { ext: ExtensionSurface; slot: Slot; host: FrameHost }) {
  const doc = ext.frame!;
  const key = `${ext.pluginId}:${ext.surfaceId}:${ext.generation ?? 0}`;
  const url = host.url(ext);
  const [phase, setPhase] = useState<Phase>({ kind: "checking" });
  const [attempt, setAttempt] = useState(0);
  const [height, setHeight] = useState<number | undefined>(clampHeight(0, SLOT_HEIGHTS[slot]));
  const channel = useRef<MessagePort | null>(null);
  const live = useRef({ ext, host, slot });
  live.current = { ext, host, slot };

  useEffect(() => {
    let current = true;
    setPhase({ kind: "checking" });
    fetch(url, { credentials: "same-origin", cache: "no-store" })
      .then(async (res) => {
        if (res.ok) return current && setPhase({ kind: "ready" });
        const body = (await res.json().catch(() => null)) as { code?: string; error?: string } | null;
        if (current) setPhase({ kind: "failed", why: say(body, t("界面文档读取失败，请重试")) });
      })
      .catch(() => current && setPhase({ kind: "failed", why: t("界面文档读取失败，请重试") }));
    return () => {
      current = false;
    };
  }, [url, attempt]);

  useEffect(() => () => channel.current?.close(), []);

  useEffect(() => {
    channel.current?.postMessage({ type: "props", props: doc.props ?? {} });
  }, [doc.props]);

  useEffect(() => {
    const observer = new MutationObserver(() => channel.current?.postMessage({ type: "theme", theme: theme() }));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "style", "class"] });
    return () => observer.disconnect();
  }, []);

  const close = () => {
    channel.current?.close();
    channel.current = null;
    setPhase({ kind: "failed", why: "" });
  };

  const connect = (iframe: HTMLIFrameElement) => {
    channel.current?.close();
    const { port1, port2 } = new MessageChannel();
    channel.current = port1;
    const gate = new RateGate();
    const drops = new DropCounter();
    port1.onmessage = (event) => {
      const now = Date.now();
      const msg = decodeFrameMessage(event.data);
      if (!msg || !gate.allow(msg.type, now)) {
        if (drops.record(now)) close();
        return;
      }
      const { ext: current, host: reach, slot: place } = live.current;
      switch (msg.type) {
        case "ready":
          port1.postMessage({
            type: "init", protocol: FRAME_PROTOCOL, props: current.frame?.props ?? {}, theme: theme(),
            locale: document.documentElement.lang || navigator.language, readOnly: !!current.code,
            reason: shown.has(key) ? "remount" : "publish", ...(saved.has(key) ? { state: saved.get(key) } : {}),
          });
          shown.add(key);
          return;
        case "size":
          setHeight(clampHeight(msg.height, SLOT_HEIGHTS[place]));
          return;
        case "action":
          if (!current.code) reach.act(current, msg.actionId, msg.args);
          return;
        case "submit":
          if (!current.code) reach.submit(current, msg.values);
          return;
        case "state":
          saved.set(key, msg.value);
          return;
      }
    };
    iframe.contentWindow?.postMessage({ type: "connect", protocol: FRAME_PROTOCOL }, "*", [port2]);
  };

  return (
    <div className="xframe" data-slot={slot} data-phase={phase.kind}>
      <div className="xframe-hd">
        <span className="nm">{doc.title}</span>
        <span className="xframe-src">{t("扩展内容")} · {ext.pluginId}</span>
      </div>
      {phase.kind === "failed" ? (
        <div className="xframe-fb" role="status">
          <p>{doc.fallback}</p>
          {phase.why && <p className="why">{phase.why}</p>}
          <button type="button" data-action="extensions.frame-retry" onClick={() => setAttempt((n) => n + 1)}>
            {t("重试")}
          </button>
        </div>
      ) : (
        phase.kind === "ready" && (
          <iframe
            key={attempt}
            className="xframe-doc"
            src={url}
            sandbox="allow-scripts"
            allow=""
            referrerPolicy="no-referrer"
            title={`${doc.title} — ${ext.pluginId}`}
            style={height === undefined ? undefined : { height }}
            onLoad={(e) => connect(e.currentTarget)}
          />
        )
      )}
    </div>
  );
}
