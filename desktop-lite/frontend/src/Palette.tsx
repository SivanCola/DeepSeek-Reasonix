import { useEffect, useMemo, useRef, useState } from "react";
import { commands as loadCommands, type Command } from "./bridge";

type Props = {
  open: boolean;
  onClose: () => void;
  onRun: (id: string) => void;
};

/**
 * The command palette, which is what lets this shell skip a settings panel:
 * capabilities are searched and invoked by name instead of each needing a
 * control with a home on screen.
 */
export default function Palette({ open, onClose, onRun }: Props) {
  const [items, setItems] = useState<Command[]>([]);
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // The catalog is re-read on every open: availability depends on whether a
  // turn is running, so a cached list would offer commands that no longer work.
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setCursor(0);
    void loadCommands().then(setItems);
    inputRef.current?.focus();
  }, [open]);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (c) => c.title.toLowerCase().includes(q) || (c.subtitle ?? "").toLowerCase().includes(q),
    );
  }, [items, query]);

  // Keep the cursor inside the list as filtering shrinks it.
  useEffect(() => {
    setCursor((c) => Math.min(c, Math.max(matches.length - 1, 0)));
  }, [matches.length]);

  if (!open) return null;

  const choose = (cmd: Command | undefined) => {
    if (!cmd || !cmd.enabled) return;
    onClose();
    onRun(cmd.id);
  };

  return (
    <div className="palette-scrim" onMouseDown={onClose}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()} role="dialog" aria-label="Commands">
        <input
          ref={inputRef}
          className="palette-input"
          value={query}
          placeholder="Type a command…"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              onClose();
            } else if (e.key === "ArrowDown") {
              e.preventDefault();
              setCursor((c) => Math.min(c + 1, matches.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setCursor((c) => Math.max(c - 1, 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              choose(matches[cursor]);
            }
          }}
        />
        <ul className="palette-list">
          {matches.length === 0 && <li className="palette-empty">No matching command</li>}
          {matches.map((cmd, i) => (
            <li
              key={cmd.id}
              className={`palette-item${i === cursor ? " active" : ""}${cmd.enabled ? "" : " disabled"}`}
              onMouseEnter={() => setCursor(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                choose(cmd);
              }}
            >
              <span className="palette-title">{cmd.title}</span>
              {cmd.subtitle && <span className="palette-subtitle">{cmd.subtitle}</span>}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
