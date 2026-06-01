import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { ModelInfo } from "../lib/types";

// ModelSwitcher is the bottom-of-window model picker: the status line's model
// label becomes a button that opens a popover (upward) listing configured
// providers. Selecting one switches the active model; the conversation is carried
// over by the backend, so the chat continues. Mirrors the "switch model, keep the
// session" behavior of comparable coding agents.
export function ModelSwitcher({ label, onPick }: { label: string; onPick: (name: string) => void | Promise<void> }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [picking, setPicking] = useState(false);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({});
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (open) app.Models().then(setModels).catch(() => {});
  }, [open]);

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const rect = triggerRef.current?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(320, Math.max(220, rect.width));
      const margin = 8;
      setMenuStyle({
        left: Math.min(Math.max(margin, rect.left), window.innerWidth - width - margin),
        bottom: Math.max(margin, window.innerHeight - rect.top + margin),
        width,
      });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open]);

  const pick = async (name: string) => {
    if (picking) return;
    setOpen(false);
    setPicking(true);
    try {
      await onPick(name);
    } finally {
      setPicking(false);
    }
  };

  return (
    <div className="modelsw">
      <button
        ref={triggerRef}
        className="modelsw__trigger"
        onClick={() => setOpen((v) => !v)}
        disabled={picking}
        title={t("status.switchModel")}
      >
        <span className="modelsw__label">{label}</span>
        <ChevronsUpDown size={11} />
      </button>
      {open && (
        <>
          <div className="modelsw__backdrop" onClick={() => setOpen(false)} />
          <div className="modelsw__menu" style={menuStyle} role="listbox">
            {models.length === 0 && <div className="modelsw__empty">{t("status.noModels")}</div>}
            {models.map((m) => (
              <button
                key={m.ref}
                role="option"
                aria-selected={m.current}
                className={`modelsw__item ${m.current ? "modelsw__item--current" : ""}`}
                onClick={() => pick(m.ref)}
              >
                <span className="modelsw__model">{m.model}</span>
                {m.current && <Check size={13} className="modelsw__check" />}
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
