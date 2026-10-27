import { useState } from "react";
import { t } from "../../i18n";
import { reason } from "../../i18n/kernel";
import type { HubPort, RuntimeView } from "../../port/hub";
import type { TeamAgent, TeamGroup, TeamView } from "../../port/team";
import { useTeam } from "../../state/team";
import { closeGroupStage, openGroupStage, useGroupStage } from "../../state/teamStage";
import { pushToast } from "../../state/toasts";
import { StudioIcon } from "../StudioIcon";
import { AgentAvatar, GroupAvatar } from "./AgentAvatar";
import { AgentDialog } from "./AgentDialog";
import { GroupDialog } from "./GroupDialog";
import "../../styles/team.css";

type Row = { kind: "agent"; agent: TeamAgent } | { kind: "group"; group: TeamGroup };

const pinnedOf = (r: Row) => (r.kind === "agent" ? !!r.agent.pinned : !!r.group.pinned);
const activeAt = (r: Row) => (r.kind === "agent" ? r.agent.lastActivityAt : r.group.lastActivityAt) ?? 0;

// The rows in the order the sidebar draws them: the first main agent leads,
// then pinned rows, then the rest by when they last moved. The kernel owns
// each field; this only orders them.
export function teamRows(view: TeamView): { lead?: TeamAgent; rows: Row[] } {
  const lead = view.agents.find((a) => a.main);
  const rows: Row[] = [
    ...view.agents.filter((a) => a !== lead && !a.hidden).map((agent) => ({ kind: "agent" as const, agent })),
    ...view.groups.filter((g) => !g.hidden && !g.archived).map((group) => ({ kind: "group" as const, group })),
  ];
  rows.sort((a, b) => Number(pinnedOf(b)) - Number(pinnedOf(a)) || activeAt(b) - activeAt(a));
  return { lead, rows };
}

function agentLine(a: TeamAgent, names: Map<string, string>): string {
  switch (a.activity.state) {
    case "needs_you":
      return t("等你处理");
    case "working":
      return t("工作中");
    case "waiting_on":
      return t("等待 {names}", { names: (a.activity.on ?? []).map((id) => names.get(id) ?? id).join("、") });
  }
  return a.preview || a.role || "";
}

function groupTitle(g: TeamGroup, names: Map<string, string>): string {
  return g.name || g.members.map((id) => names.get(id) ?? id).join("、");
}

function groupLine(g: TeamGroup, names: Map<string, string>): string {
  if (g.round.parked) return t("已暂停，等你继续");
  if (g.round.speaking) return t("{name} 正在发言", { name: names.get(g.round.speaking) ?? g.round.speaking });
  return g.preview ?? "";
}

// The team's block in the sidebar: the agents and the group chats they meet
// in. Opening an agent opens its own conversation as a pane; opening a group
// fills the main area with that group's chat.
export function TeamRail({ hub, active, onAdoptPane }: { hub: HubPort; active: string; onAdoptPane: (rt: RuntimeView) => void }) {
  const { view, unavailable, team, shown } = useTeam(hub);
  const stage = useGroupStage();
  const [dialog, setDialog] = useState<{ kind: "agent" | "group"; id?: string } | null>(null);
  const [starting, setStarting] = useState(false);
  if (unavailable || !view || !team) return null;
  const names = new Map(view.agents.map((a) => [a.id, a.name]));
  const { lead, rows } = teamRows(view);
  const readOnly = !!view.readOnly;

  const openAgent = async (id: string) => {
    try {
      const rt = await team.openAgent(id);
      closeGroupStage();
      onAdoptPane(rt);
    } catch (e) {
      pushToast(reason(e));
    }
  };
  const bootstrap = async () => {
    setStarting(true);
    try {
      const next = await team.bootstrap("Chief", t("首席助理"));
      shown(next);
      const chief = next.agents.find((a) => a.main);
      if (chief) await openAgent(chief.id);
    } catch (e) {
      pushToast(reason(e));
    } finally {
      setStarting(false);
    }
  };
  const agentRow = (a: TeamAgent, main = false) => (
    <li key={a.id} className={main ? "team-row team-row--main" : "team-row"} data-state={a.activity.state} aria-current={!!a.runtime && a.runtime === active && !stage}>
      <button type="button" className="team-row-open" data-action="team.agent-open" data-target={a.id} onClick={() => void openAgent(a.id)}>
        <AgentAvatar agent={a} size={main ? 36 : 28} />
        <span className="team-row-text">
          <span className="team-row-name">{a.name}</span>{" "}
          <span className="team-row-line">{agentLine(a, names)}</span>
        </span>
      </button>
      {a.pinned && <StudioIcon name="pin" className="team-row-pin" aria-label={t("已置顶")} />}
      {!readOnly && (
        <button type="button" className="team-row-more" data-action="team.agent-settings" data-target={a.id} aria-label={t("{name} 的设置", { name: a.name })}
          onClick={() => setDialog({ kind: "agent", id: a.id })}>
          <StudioIcon name="more" />
        </button>
      )}
    </li>
  );
  const groupRow = (g: TeamGroup) => (
    <li key={g.id} className="team-row" aria-current={stage === g.id}>
      <button type="button" className="team-row-open" data-action="team.group-open" data-target={g.id} onClick={() => openGroupStage(g.id)}>
        <GroupAvatar members={g.members.map((id) => view.agents.find((a) => a.id === id)).filter((a): a is TeamAgent => !!a)} />
        <span className="team-row-text">
          <span className="team-row-name">{groupTitle(g, names)}</span>{" "}
          <span className="team-row-line">{groupLine(g, names)}</span>
        </span>
      </button>
      {g.pinned && <StudioIcon name="pin" className="team-row-pin" aria-label={t("已置顶")} />}
      {!readOnly && (
        <button type="button" className="team-row-more" data-action="team.group-settings" data-target={g.id} aria-label={t("{name} 的设置", { name: groupTitle(g, names) })}
          onClick={() => setDialog({ kind: "group", id: g.id })}>
          <StudioIcon name="more" />
        </button>
      )}
    </li>
  );
  const editing = dialog?.kind === "agent" ? view.agents.find((a) => a.id === dialog.id) : undefined;
  const editingGroup = dialog?.kind === "group" ? view.groups.find((g) => g.id === dialog.id) : undefined;
  const done = (next: TeamView) => {
    shown(next);
    setDialog(null);
  };

  return (
    <section className="team-rail" aria-label={t("团队")}>
      <div className="team-rail-head">
        <h2>{t("团队")}</h2>
        {!readOnly && view.agents.length > 0 && (
          <>
            <button type="button" data-action="team.agent-new" aria-label={t("新建 Agent")} title={t("新建 Agent")} onClick={() => setDialog({ kind: "agent" })}>
              <StudioIcon name="plus" />
            </button>
            <button type="button" data-action="team.group-new" aria-label={t("新建群聊")} title={t("新建群聊")} disabled={view.agents.length < 2}
              onClick={() => setDialog({ kind: "group" })}>
              <StudioIcon name="chat" />
            </button>
          </>
        )}
      </div>
      {readOnly && <p className="team-note">{t("团队正在另一个 Reasonix 窗口中编辑，这里只能查看")}</p>}
      {view.agents.length === 0 ? (
        !readOnly && (
          <div className="team-welcome">
            <h3>{t("组建你的团队")}</h3>
            <p>{t("Chief 是你的主 Agent：它可以按你的要求创建其他 Agent 和群聊，并把工作分派给它们。每个 Agent 都有自己的长期对话。")}</p>
            <button type="button" className="team-primary" data-action="team.bootstrap" disabled={starting} onClick={() => void bootstrap()}>
              {starting ? t("正在创建…") : t("创建 Chief")}
            </button>
          </div>
        )
      ) : (
        <ul className="team-rows">
          {lead && agentRow(lead, true)}
          {rows.map((r) => (r.kind === "agent" ? agentRow(r.agent) : groupRow(r.group)))}
        </ul>
      )}
      {dialog?.kind === "agent" && (dialog.id === undefined || editing) && (
        <AgentDialog team={team} agent={editing} onClose={() => setDialog(null)} onSaved={done} />
      )}
      {dialog?.kind === "group" && (dialog.id === undefined || editingGroup) && (
        <GroupDialog team={team} agents={view.agents} group={editingGroup} onClose={() => setDialog(null)} onSaved={done} />
      )}
    </section>
  );
}
