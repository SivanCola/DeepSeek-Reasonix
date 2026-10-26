import { useRef, useState } from "react";
import { t } from "../i18n";
import type { ExtensionSurface, ExtensionViewNode } from "../port/wire";
import { ExtensionView } from "./cards/ExtensionView";
import { SLOTS, fits, placement } from "./slots";
import { FrameView, type FrameHost } from "./frame/FrameView";

// A view plus the one control the host adds to it: where it goes. The handle is
// the host's, not the extension's — an extension asks for a place, and this is
// where the user overrules it. It stays hidden until the surface is hovered,
// because the common case is reading the thing, not moving it.
export function SlottedView({
  ext, body, assigned = {}, frames, onAction, onMove,
}: {
  ext: ExtensionSurface;
  // What a narrow place can draw of the view, when it cannot draw all of it.
  body?: ExtensionViewNode[];
  assigned?: Record<string, string>;
  // Where a frame surface reaches its document and its plugin; without it a
  // frame shows its fallback text.
  frames?: FrameHost;
  onAction: (actionId: string) => void;
  onMove: (slot: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const grip = useRef<HTMLButtonElement>(null);
  const here = placement(ext, assigned);
  // Hiding the menu takes the row that had focus with it, and the caret drops
  // to the document — so closing hands it back the way Menu does.
  const close = (then: () => void) => { setOpen(false); grip.current?.focus(); then(); };
  return (
    <div className="slotted">
      {ext.kind !== "frame" ? (
        <ExtensionView body={body ?? ext.view?.body ?? []} onAction={onAction} />
      ) : frames ? (
        <FrameView ext={ext} slot={here} host={frames} />
      ) : (
        <p className="xframe-fb">{ext.frame?.fallback}</p>
      )}
      <button
        ref={grip}
        className="movegrip"
        aria-label={t("移动 {id} 的界面", { id: ext.pluginId })}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        ⋯
      </button>
      {/* hidden rather than conditional: the menu has to stay in the DOM to have
          anything to animate on the way out. */}
      <div className="movemenu" role="menu" hidden={!open}>
        <span className="who">{ext.pluginId}</span>
        {SLOTS.filter((slot) => fits(slot.id, ext)).map((slot) => (
          <button
            key={slot.id}
            role="menuitemradio"
            data-action="panel.move"
            aria-checked={here === slot.id}
            onClick={() => close(() => onMove(slot.id))}
          >
            {t(slot.label)}
          </button>
        ))}
        {/* Clearing hands the choice back to the extension rather than
            hiding the surface: it is a different act from moving it. */}
        <button role="menuitem" data-action="panel.delegate-placement" onClick={() => close(() => onMove(""))}>
          {t("交还给插件")}
        </button>
      </div>
    </div>
  );
}
