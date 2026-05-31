import { Download, Minus, Plus, X } from "lucide-react";
import { useState } from "react";

export function ImagePreview({
  src,
  label,
  onClose,
}: {
  src: string;
  label: string;
  onClose: () => void;
}) {
  const [zoom, setZoom] = useState(1);
  const pct = Math.round(zoom * 100);
  const download = () => {
    const a = document.createElement("a");
    a.href = src;
    a.download = label.split("/").pop() || "image.png";
    a.click();
  };

  return (
    <div className="imgpreview" role="dialog" aria-modal="true" onMouseDown={onClose}>
      <div className="imgpreview__actions" onMouseDown={(e) => e.stopPropagation()}>
        <button className="imgpreview__round" type="button" onClick={download} title="Download">
          <Download size={20} />
        </button>
        <button className="imgpreview__round" type="button" onClick={onClose} title="Close">
          <X size={22} />
        </button>
      </div>
      <img
        className="imgpreview__image"
        src={src}
        alt={label}
        style={{ transform: `scale(${zoom})` }}
        onMouseDown={(e) => e.stopPropagation()}
      />
      <div className="imgpreview__zoom" onMouseDown={(e) => e.stopPropagation()}>
        <button type="button" onClick={() => setZoom((z) => Math.max(0.25, z - 0.25))} title="Zoom out">
          <Minus size={18} />
        </button>
        <span>{pct}%</span>
        <button type="button" onClick={() => setZoom((z) => Math.min(3, z + 0.25))} title="Zoom in">
          <Plus size={18} />
        </button>
      </div>
    </div>
  );
}
