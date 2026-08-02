import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, X } from "lucide-react";
import type { AppBindings } from "../lib/bridge";
import type { ConfigRepairView } from "../lib/types";

type ConfigRepairAPI = Pick<AppBindings,
  "ConfigRepairStatus" | "UndoConfigRepair" | "RestoreGlobalConfigSnapshot" | "OpenConfigFile"
>;

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
export function ConfigRepairBanner({ api }: { api: ConfigRepairAPI }) {
  const [view, setView] = useState<ConfigRepairView>(emptyView);
  const [dismissed, setDismissed] = useState(false);
  const [actionError, setActionError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api.ConfigRepairStatus()
      .then((v: ConfigRepairView | null | undefined) => {
        if (!cancelled) {
          setView(v?.outcome ? v : emptyView);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [api]);

  useEffect(() => {
    if (view.outcome !== "config_damaged") {
      return;
    }
    let cancelled = false;
    const refresh = () => {
      api.ConfigRepairStatus()
        .then((v) => {
          if (!cancelled) {
            setView(v?.outcome ? v : emptyView);
          }
        })
        .catch(() => {});
    };
    const timer = window.setInterval(refresh, 2000);
    window.addEventListener("focus", refresh);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      window.removeEventListener("focus", refresh);
    };
  }, [api, view.outcome]);

  if (!view.outcome || dismissed) {
    return null;
  }
  const damaged = view.outcome === "config_damaged";

  const onUndo = () => {
    const transactionID = view.transactionId?.trim();
    if (!transactionID) {
      setActionError("撤销操作已过期，请重新检查配置");
      return;
    }
    setActionError("");
    api.UndoConfigRepair(transactionID)
      .then(() => setDismissed(true))
      .catch(() => setActionError("撤销操作已过期，请重新检查配置"));
  };
  const onRestore = () => {
    api.RestoreGlobalConfigSnapshot()
      .then((ok: boolean) => {
        if (ok) {
          setView(emptyView);
        }
      })
      .catch(() => {});
  };
  const onOpenFile = () => {
    api.OpenConfigFile().catch(() => {});
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
        {actionError && <span className="config-repair-banner__sub">{actionError}</span>}
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
