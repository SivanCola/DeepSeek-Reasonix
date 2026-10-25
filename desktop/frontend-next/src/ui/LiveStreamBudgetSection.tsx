import { useEffect, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { liveStreamBudget, MAX_LIVE_STREAM_BUDGET, onLiveStreamBudgetChange, setLiveStreamBudget } from "../state/prefs";
import { Group } from "./Group";

// How many open session tabs may keep a live connection at once. Empty is
// automatic. A browser holds six connections to one address and the session list
// takes one, so the window keeps a few live and reads the rest when they are shown.
export function LiveStreamBudgetSection() {
  const chosen = useSyncExternalStore(onLiveStreamBudgetChange, liveStreamBudget, liveStreamBudget);
  const [draft, setDraft] = useState(chosen === null ? "" : String(chosen));
  useEffect(() => setDraft(chosen === null ? "" : String(chosen)), [chosen]);
  const commit = () => {
    setLiveStreamBudget(draft === "" ? null : Number(draft));
    const now = liveStreamBudget();
    setDraft(now === null ? "" : String(now));
  };
  return (
    <Group id="live-stream-budget" title={t("保持实时连接的会话标签数")} hint={t("留空表示自动；可填 1 到 {max}。其余后台标签按需更新。", { max: MAX_LIVE_STREAM_BUDGET })}
      action={
        <span className="unit-field">
          <input
            id="live-stream-budget-input"
            aria-label={t("实时连接的标签数")}
            type="text"
            inputMode="numeric"
            placeholder={t("自动")}
            value={draft}
            onChange={(e) => setDraft(e.target.value.replace(/\D/g, ""))}
            onBlur={commit}
            data-action-change="appearance.live-stream-budget"
            data-action-keydown="appearance.live-stream-budget"
            onKeyDown={(e) => {
              if (e.key !== "Enter") return;
              commit();
              e.currentTarget.blur();
            }}
          />
          <i>{t("个")}</i>
        </span>
      } />
  );
}
