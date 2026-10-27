import { t } from "../i18n";
import type { MenuItem } from "./Menu";

/** One chip per provider the model menu lists; the chosen ones narrow it. Drawn
 *  only where there is a choice to make, so a single provider shows nothing. */
export function ModelGroupChips({ headings, chosen, onChange }: { headings: MenuItem[]; chosen: readonly string[]; onChange: (next: string[]) => void }) {
  if (headings.length < 2) return null;
  return (
    <div className="mchips" role="group" aria-label={t("按服务商筛选")}>
      <button type="button" className="mchip" data-action="model.group-clear" aria-pressed={chosen.length === 0} onClick={() => onChange([])}>{t("全部")}</button>
      {headings.map((h) => {
        const on = chosen.includes(h.value);
        return (
          <button
            key={h.value}
            type="button"
            className="mchip"
            data-action="model.group"
            data-target={h.value}
            aria-pressed={on}
            onClick={() => onChange(on ? chosen.filter((key) => key !== h.value) : [...chosen, h.value])}
          >
            {h.label}
          </button>
        );
      })}
      {chosen.length > 0 && (
        <button type="button" className="mchip clear" data-action="model.group-clear" onClick={() => onChange([])}>
          {t("清除")}
        </button>
      )}
    </div>
  );
}
