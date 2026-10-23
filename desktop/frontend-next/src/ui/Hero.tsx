import { t } from "../i18n";
import { SymbolMark } from "./RMark";

interface HeroProps {
  needsProject: boolean;
  onOpen: () => void;
  onKeep: () => void;
}

export function Hero({ needsProject, onOpen, onKeep }: HeroProps) {
  return (
    <div className="hero">
      <SymbolMark />
      <div className="t">{needsProject ? t("先打开一个项目") : t("描述任务，其余交给 Reasonix")}</div>
      <div className="s">
        {needsProject
          ? t("读取代码、运行测试与修改文件均只在你选定的文件夹内进行。")
          : t("可读取代码、查找资料、运行工具并修改文件。完整执行过程可在「运行分析」中查看。")}
      </div>
      {needsProject ? (
        <div className="herogo">
          <button className="pick" data-action="workspace.add" onClick={onOpen}>
            {t("打开项目…")}
          </button>
          <button className="stay" onClick={onKeep}>
            {t("使用当前位置")}
          </button>
        </div>
      ) : null}
    </div>
  );
}
