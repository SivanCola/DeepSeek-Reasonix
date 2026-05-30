import { useEffect, useRef, useState } from "react";

export interface JumpBarItem {
  index: number;
  turn: number;
  text: string;
}

interface JumpBarProps {
  activeTurn: number | null;
  items: JumpBarItem[];
  onJump: (item: JumpBarItem) => void;
}

export function JumpBar({ activeTurn, items, onJump }: JumpBarProps) {
  const [hovered, setHovered] = useState<number | null>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const previewTop = useRef(0);
  const [showPreview, setShowPreview] = useState(false);

  useEffect(() => {
    if (activeTurn === null) return;
    const el = barRef.current?.querySelector(`[data-turn="${activeTurn}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeTurn]);

  if (items.length < 2) return null;

  const hoverIdx = hovered !== null ? items.findIndex((v) => v.turn === hovered) : -1;
  const hoverText = hovered !== null ? items.find((v) => v.turn === hovered)?.text : null;

  const onMove = (e: React.MouseEvent) => {
    const el = barRef.current;
    if (!el) return;
    const q = el.querySelectorAll<HTMLElement>(".jump-item");
    const barRect = el.getBoundingClientRect();
    let closest = -1;
    let closestDist = Number.POSITIVE_INFINITY;
    q.forEach((item, i) => {
      const r = item.getBoundingClientRect();
      const midY = r.top + r.height / 2;
      const dist = Math.abs(e.clientY - midY);
      if (dist < closestDist) {
        closestDist = dist;
        closest = i;
        previewTop.current = midY - barRect.top;
      }
    });
    if (closest >= 0 && closest < items.length) {
      const turn = items[closest]?.turn;
      if (turn !== undefined) {
        setHovered(turn);
        setShowPreview(true);
      }
    }
  };

  const dotProps = (
    idx: number,
    turn: number,
  ): { style: React.CSSProperties; "data-d"?: string } => {
    const isActive = activeTurn === turn;
    if (hoverIdx < 0) {
      return {
        style: { width: isActive ? 18 : 12, background: isActive ? "var(--accent)" : undefined },
      };
    }
    const d = Math.abs(idx - hoverIdx);
    const width = d === 0 ? 32 : d === 1 ? 20 : d === 2 ? 14 : isActive ? 18 : 12;
    const background = d <= 2 ? undefined : isActive ? "var(--accent)" : undefined;
    return {
      style: { width, transitionDelay: `${d * 20}ms`, background },
      "data-d": d <= 2 ? String(d) : undefined,
    };
  };

  return (
    <div
      className="jump-bar"
      ref={barRef}
      onMouseMove={onMove}
      onMouseLeave={() => {
        setHovered(null);
        setShowPreview(false);
      }}
    >
      <div className="jump-scroll">
        {items.map((item, idx) => (
          <button
            type="button"
            className="jump-item"
            key={item.turn}
            data-turn={item.turn}
            onClick={() => onJump(item)}
            title={item.text}
            aria-label={`Jump to turn ${item.turn}: ${item.text}`}
          >
            <span className="jump-dot" {...dotProps(idx, item.turn)} />
          </button>
        ))}
      </div>
      {showPreview && hoverText && (
        <div className="jump-preview" style={{ top: previewTop.current }}>
          <span className="jump-text">{hoverText}</span>
        </div>
      )}
    </div>
  );
}
