import { t } from "../i18n";
import { SETTING_AT, type SettingScope, type ApplySemantics } from "./prefsnav";
import { createContext, useContext, useId, type ReactNode } from "react";

type SharedSemantics = { ids: string[]; noteId: string; hasHeading: boolean; scope?: SettingScope; apply?: ApplySemantics };
const Shared = createContext<SharedSemantics | null>(null);

export function SettingSection({ title, ids, children }: { title?: string; ids: string[]; children: ReactNode }) {
  const noteId = useId();
  const entries = ids.map(SETTING_AT);
  const first = entries[0];
  const complete = entries.length > 1 && entries.every(Boolean);
  const scope = complete && entries.every((e) => e?.scope === first?.scope) ? first?.scope : undefined;
  const apply = complete && first?.apply !== "none" && entries.every((e) => e?.apply === first?.apply) ? first?.apply : undefined;
  const value = { ids, noteId, hasHeading: !!title, scope, apply };
  return <section className="settings-group">
    {(title || scope || apply) && <header className="settings-group-heading">
      {title && <h3>{title}</h3>}
      {(scope || apply) && <p id={noteId} className="settings-group-note">
        {[scope && t(SCOPE_SAID[scope]), apply && t(APPLY_SAID[apply])].filter(Boolean).join(" · ")}
      </p>}
    </header>}
    <Shared.Provider value={value}>{children}</Shared.Provider>
  </section>;
}

function useShared(id: string) {
  const group = useContext(Shared);
  return group?.ids.includes(id) ? group : null;
}

// id is required, and that is the whole enforcement: a settings block cannot
// be written without naming itself, and a name the catalogue does not carry
// fails the gate. So "which setting is this, and when does changing it take
// effect" has no default and no way to be left unanswered.
export function Group({
  id, title, hint, now, action, children, saveState, variant = "card",
}: {
  id: string; title: string; hint?: string; now?: string; action?: ReactNode; children?: ReactNode;
  saveState?: "dirty" | "saving" | "saved";
  variant?: "card" | "note" | "section" | "toolbar";
}) {
  const shared = useShared(id);
  const Heading = shared?.hasHeading ? "h4" : "h3";
  return (
    <section className={variant === "card" ? "grp" : `setting-${variant}`} id={`set-${id}`} data-setting={id}
      aria-describedby={shared?.scope || shared?.apply ? shared.noteId : undefined}>
      <div className="grp-hd">
        <Heading>{title}</Heading>
        {now && <span className="now">{now}</span>}
        {action}
      </div>
      {hint && <p className="hint">{hint}</p>}
      {children && <div className="grp-items">{children}</div>}
      {/* A fact about this block, at the weight of a fact: what it costs to
          change is not a warning, and three of them on a page should not read
          as three alarms. */}
      <ApplyNote id={id} saveState={saveState} />
    </section>
  );
}

/** Who a change reaches and when it is in force. Separate from Group so a block
 *  that draws its own frame still cannot go without one — the appearance page
 *  builds its sections by hand, and the questions are the same there.
 *
 *  Both halves come from the catalogue and neither is written at a call site:
 *  one table answers for the block and for the search result that leads to it,
 *  so the two can never disagree about what a setting owns. */
export function ApplyNote({ id, saveState }: { id: string; saveState?: "dirty" | "saving" | "saved" }) {
  const shared = useShared(id);
  const entry = SETTING_AT(id);
  if (!entry) return null;
  const scope = shared?.scope === entry.scope ? null : t(SCOPE_SAID[entry.scope]);
  const apply = entry.apply === "none" || shared?.apply === entry.apply ? null : t(APPLY_SAID[entry.apply]);
  if (!scope && !apply && !saveState) return <span className="setting-inherited" data-apply={entry.apply} data-scope={entry.scope} aria-describedby={shared?.noteId} />;
  // One text node: .apply is a flex row, and separate children would take the
  // row's gap on both sides of the separator.
  return (
    <p className="apply" aria-live={saveState ? "polite" : undefined} data-apply={entry.apply} data-scope={entry.scope}>
      {[scope, saveState && t({ dirty: "未保存", saving: "保存中…", saved: "已保存" }[saveState]), apply].filter(Boolean).join(" · ")}
    </p>
  );
}

/** Whose setting this is. A fact, at the weight of a fact — no accent, no chip:
 *  ownership is not a warning, and thirty of them down a page must not read as
 *  thirty alarms. */
export const SCOPE_SAID: Record<SettingScope, string> = {
  session: "当前会话",
  model: "当前模型",
  workspace: "当前工作区",
  machine: "本机",
  account: "账号",
  chosen: "写入位置由你选择",
};

const APPLY_SAID: Record<"immediate" | "runtime-rebuild" | "restart" | "on-open" | "next-turn" | "next-rebuild", string> = {
  immediate: "立即生效",
  "runtime-rebuild": "重建运行时",
  restart: "重启后生效",
  "on-open": "新开的会话生效",
  "next-turn": "下个回合起生效",
  "next-rebuild": "下次会话构建时生效",
};
