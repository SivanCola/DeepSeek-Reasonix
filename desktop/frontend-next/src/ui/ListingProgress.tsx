import type { TreeWorkspace } from "../port/hub";
import { t } from "../i18n";

export function ListingProgress({ workspace, searching, onRetry }: { workspace: TreeWorkspace; searching: boolean; onRetry?: () => void }) {
  if (workspace.countsComplete !== false) return null;
  const failed = workspace.listingState === "degraded";
  return <small className="sessnote" role="status" aria-busy={!failed}>
    {failed ? t("部分历史摘要暂不可用，可继续打开会话") : searching ? t("仍在搜索部分历史会话…") : t("正在读取历史摘要…")}
    {failed && onRetry && <button type="button" className="ws-reading-retry" data-action="tree.retry" onClick={onRetry}>{t("重试")}</button>}
  </small>;
}
