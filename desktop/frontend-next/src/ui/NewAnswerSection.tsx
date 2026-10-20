import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { onNewAnswerMarkerChange, setShowsNewAnswerMarker, showsNewAnswerMarker } from "../state/newAnswers";
import { Group } from "./Group";

export function NewAnswerSection() {
  const on = useSyncExternalStore(onNewAnswerMarkerChange, showsNewAnswerMarker, showsNewAnswerMarker);
  return (
    <Group id="new-answer-marker" title={t("新回答分隔线")} hint={t("打开有未读回答的会话时，在回答开始的位置画一条分隔线。只在这个窗口里记着，重启后不再显示。")}>
      <div className="seg" data-text role="group" aria-label={t("新回答分隔线")}>
        <button data-action="appearance.new-answer-marker" data-value="off" aria-pressed={!on} onClick={() => setShowsNewAnswerMarker(false)}>{t("关闭")}</button>
        <button data-action="appearance.new-answer-marker" data-value="on" aria-pressed={on} onClick={() => setShowsNewAnswerMarker(true)}>{t("显示")}</button>
      </div>
    </Group>
  );
}
