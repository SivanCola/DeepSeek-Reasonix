import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, X } from "lucide-react";

interface ConfigRepairView {
  outcome: string; // "auto_fixed" | "restored_snapshot" | "safe_mode" | "config_damaged" | ""
  scope: string;
  path: string;
  detail: string;
  repairedAt: string;
  undoable: boolean;
  canOpenFile: boolean;
}

const emptyView: ConfigRepairView = {
  outcome: "",
  scope: "",
  path: "",
  detail: "",
  repairedAt: "",
  undoable: false,
  canOpenFile: false,
};

/**
 * Recovery banner for global-config outcomes:
 * - "auto_fixed": the Guard repaired Windows paths at startup; offers undo.
 * - "config_damaged": the global config failed to load and the app runs on
 *   the recovery configuration; stays visible until the config is repaired.
 */
export function ConfigRepairBanner() {
  const [view, setView] = useState<ConfigRepairView>(emptyView);
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (window as any).go?.main?.App?.ConfigRepairStatus?.()
      .then((v: ConfigRepairView | null | undefined) => {
        if (!cancelled && v && v.outcome) {
          setView(v);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  if (!view.outcome || dismissed) {
    return null;
  }
  const damaged = view.outcome === "config_damaged";

  const onUndo = () => {
    (window as any).go?.main?.App?.UndoConfigRepair?.()
      .then(() => setDismissed(true))
      .catch(() => setDismissed(true));
  };
  const onRestore = () => {
    (window as any).go?.main?.App?.RestoreGlobalConfigSnapshot?.()
      .then((ok: boolean) => {
        if (ok) {
          setView(emptyView);
        }
      })
      .catch(() => {});
  };
  const onOpenFile = () => {
    (window as any).go?.main?.App?.OpenConfigFile?.().catch(() => {});
  };

  return (
    <div
      className={damaged ? "config-repair-banner config-repair-banner--damaged" : "config-repair-banner"}
      role={damaged ? "alert" : "status"}
    >
      {damaged ? (
        <AlertTriangle className="config-repair-banner__icon" size={16} aria-hidden />
      ) : (
        <CheckCircle2 className="config-repair-banner__icon" size={16} aria-hidden />
      )}
      <span className="config-repair-banner__text">
        {view.detail}
        {view.undoable && <span className="config-repair-banner__sub">备份已保留，可撤销</span>}
      </span>
      {view.undoable && (
        <button type="button" className="config-repair-banner__action" onClick={onUndo}>
          撤销修复
        </button>
      )}
      {damaged && (
        <button type="button" className="config-repair-banner__action" onClick={onRestore}>
          从备份恢复
        </button>
      )}
      {view.canOpenFile && (
        <button type="button" className="config-repair-banner__action" onClick={onOpenFile}>
          打开配置文件
        </button>
      )}
      {!damaged && (
        <button
          type="button"
          className="config-repair-banner__close"
          onClick={() => setDismissed(true)}
          aria-label="关闭"
        >
          <X size={14} aria-hidden />
        </button>
      )}
    </div>
  );
}
