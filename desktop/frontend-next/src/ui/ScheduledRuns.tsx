import { useCallback, useEffect, useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import { HttpError } from "../port/http_error";
import type { AgentPort, ScheduleBoard, ScheduleDraft, SchedulePreview, ScheduleResultView, ScheduleRunView, ScheduleView } from "../port/port";
import { Group } from "./Group";

const EVERY: [number, string][] = [[3600, "每小时"], [7200, "每 2 小时"], [21600, "每 6 小时"], [43200, "每 12 小时"], [86400, "每天"], [604800, "每周"]];
const POLL_MS = 30_000;

const when = (unix: number) => new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
const base = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path;

function trigger(s: ScheduleView): string {
  if (s.atUnix) return t("一次：{at}", { at: when(s.atUnix) });
  const hit = EVERY.find(([sec]) => sec === s.everySec);
  return hit ? t(hit[1]) : t("每 {n} 分钟", { n: Math.round((s.everySec ?? 0) / 60) });
}

const STATUS: Record<string, string> = { active: "运行中", paused: "已暂停", expired: "已到期", "circuit-open": "已熔断" };
const PAUSE: Record<string, string> = { user: "你暂停了它", consecutive_failures: "连续失败后自动暂停", breaker: "额度保护自动暂停", lifetime_budget: "用完了总额度", corrupt: "记录损坏" };
const RUN: Record<string, string> = {
  succeeded: "已完成", failed: "失败", blocked: "等你决定", budget_stopped: "额度用尽", interrupted: "被中断", skipped: "已跳过", claimed: "即将开始", running: "运行中",
};
const SKIP: Record<string, string> = { missed: "错过了时间", budget_window: "额度窗口已满", lease_held: "有任务在运行", concurrency: "有任务在运行" };

/** Read-only tasks a person confirmed, run at a time they chose. A creation is
 *  shown back as a preview with the most it can spend, and only that preview's
 *  own confirmation creates it. A report is the model's text: it is labelled as
 *  such and drawn as plain text, never as markup. */
export function ScheduledRuns({ port }: { port: AgentPort }) {
  const [board, setBoard] = useState<ScheduleBoard | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [form, setForm] = useState<ScheduleDraft | null>(null);
  const [preview, setPreview] = useState<SchedulePreview | null>(null);
  const [confirmDelete, setConfirmDelete] = useState("");
  const [result, setResult] = useState<ScheduleResultView | null>(null);

  const [loadError, setLoadError] = useState("");

  // A failed read keeps the last board and says why: the block vanishing would
  // leave a person who just created a schedule unable to tell whether it exists.
  const refresh = useCallback(
    () => port.schedules().then((b) => { setBoard(b); setLoadError(""); }).catch((e) => setLoadError(reason(e))),
    [port],
  );
  useEffect(() => {
    let alive = true;
    const load = () => { if (alive) void refresh(); };
    load();
    const id = setInterval(load, POLL_MS);
    return () => { alive = false; clearInterval(id); };
  }, [refresh]);

  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      await refresh();
    } catch (e) {
      if (e instanceof HttpError && e.reason?.code === "schedule.editing_disabled") setReadOnly(true);
      setError(reason(e));
    } finally {
      setBusy(false);
    }
  }, [refresh]);

  const unreadable = loadError && (
    <div className="peerform" role="alert">
      <div className="why">{board ? t("无法刷新定时运行：{reason}（下面是上一次读到的内容）", { reason: loadError }) : t("无法读取定时运行：{reason}", { reason: loadError })}</div>
      <span className="acts"><button className="act" data-action="schedule.retry" onClick={() => void refresh()}>{t("重试")}</button></span>
    </div>
  );
  if (!board) {
    return unreadable ? (
      <Group id="scheduled-runs" title={t("定时运行")} hint={t("按你确认的时间，只读地运行任务：它能读这个文件夹里的文件，不能写任何东西；需要你批准的事会挂起等你，不会替你答复。")}>
        <div className="box">{unreadable}</div>
      </Group>
    ) : null;
  }
  const locked = busy || readOnly;
  const name = (s: ScheduleView) => s.label || base(s.workspace);
  const scheduleName = (r: ScheduleRunView) => { const s = board.schedules.find((x) => x.id === r.scheduleId); return s ? name(s) : r.scheduleId; };
  const draft = (patch: Partial<ScheduleDraft>) => setForm((f) => (f ? { ...f, ...patch } : f));

  return (
    <Group id="scheduled-runs" title={t("定时运行")} hint={t("按你确认的时间，只读地运行任务：它能读这个文件夹里的文件，不能写任何东西；需要你批准的事会挂起等你，不会替你答复。")}>
      <div className="box">
        {unreadable}
        <div className="lrow">
          <span className="tx">
            <span className="lb">{board.pausedAll ? t("全部定时运行已暂停") : t("定时运行进行中")}</span>
            <span className="ds">{t("最多 {n} 个；每次至少间隔 {m} 分钟；每天合计最多 {k} 个令牌。", { n: board.policy.maxSchedules, m: board.policy.minIntervalMinutes, k: board.policy.globalTokensDay.toLocaleString() })}</span>
          </span>
          {board.pausedAll ? (
            <button className="act" data-action="schedule.resume-all" disabled={locked} onClick={() => void run(() => port.setSchedulesPaused(false))}>{t("全部恢复")}</button>
          ) : (
            <button className="act" data-action="schedule.pause-all" disabled={busy} onClick={() => void run(() => port.setSchedulesPaused(true))}>{t("全部暂停")}</button>
          )}
        </div>

        {board.schedules.length === 0 && <div className="peernote">{t("还没有定时运行。")}</div>}
        <ul className="wa-list" aria-label={t("定时运行列表")}>
          {board.schedules.map((s) => (
            <li key={s.id}>
              <span className="tx">
                <span className="lb">{name(s)}</span>
                <span className="ds">{trigger(s)} · {t(STATUS[s.status] ?? s.status)}{s.pausedReason ? " · " + t(PAUSE[s.pausedReason] ?? s.pausedReason) : ""}</span>
                <span className="ds">{s.workspace} · {s.model}</span>
                <span className="ds">{t("已用 {a} / {b} 令牌，{d} 到期", { a: s.spentLifetimeTokens.toLocaleString(), b: s.lifetimeTokens.toLocaleString(), d: when(s.expiresAtUnix) })}</span>
              </span>
              <span className="acts">
                {s.status === "active" ? (
                  <button className="act" data-action="schedule.pause" data-target={s.id} disabled={busy} onClick={() => void run(() => port.scheduleAction(s.id, "pause"))}>{t("暂停")}</button>
                ) : (
                  <button className="act" data-action="schedule.resume" data-target={s.id} disabled={locked} onClick={() => void run(() => port.scheduleAction(s.id, "resume"))}>{t("恢复")}</button>
                )}
                <button className="act" data-action="schedule.renew" data-target={s.id} disabled={locked} onClick={() => void run(() => port.scheduleAction(s.id, "renew"))}>{t("续期")}</button>
                {confirmDelete === s.id ? (
                  <button className="act danger" data-action="schedule.delete" data-target={s.id} disabled={busy} onClick={() => void run(async () => { await port.scheduleAction(s.id, "delete"); setConfirmDelete(""); })}>{t("确认删除")}</button>
                ) : (
                  <button className="act ghost" data-action="schedule.delete-ask" data-target={s.id} disabled={busy} onClick={() => setConfirmDelete(s.id)}>{t("删除")}</button>
                )}
              </span>
            </li>
          ))}
        </ul>

        {board.runs.length > 0 && (
          <>
            <div className="peernote"><b>{t("最近的运行")}</b></div>
            <ul className="wa-list" aria-label={t("最近的运行")}>
              {board.runs.slice(0, 8).map((r) => (
                <li key={r.triggerId}>
                  <span className="tx">
                    <span className="lb">{scheduleName(r)} · {t(RUN[r.state] ?? r.state)}</span>
                    <span className="ds">{when(r.slotUnix)} · {r.chargedTokens.toLocaleString()} {t("令牌")}{r.skipReason ? " · " + t(SKIP[r.skipReason] ?? r.skipReason) : ""}</span>
                  </span>
                  {r.state !== "skipped" && r.state !== "claimed" && (
                    <button className="act" data-action="schedule.result" data-target={r.triggerId} disabled={busy} onClick={() => void run(async () => setResult(await port.scheduleResult(r.triggerId)))}>{t("查看结果")}</button>
                  )}
                </li>
              ))}
            </ul>
          </>
        )}

        {result && (
          <div className="peerform" role="region" aria-label={t("运行结果")}>
            <div className="peernote"><b>{t("运行结果")}</b> · {when(result.recordedUnix)}</div>
            <div className="peernote">{t("以下是模型写的内容，只作参考，不是 Reasonix 的说明。")}</div>
            <pre className="peerbody" data-testid="schedule-report">{result.report}</pre>
            {result.reportTruncated && <div className="peernote">{t("报告太长，已截断。")}</div>}
            {result.pending.length > 0 && (
              <>
                <div className="peernote"><b>{t("等你决定的事")}</b></div>
                <div className="peernote">{t("以下是模型写的内容，只作参考，不是 Reasonix 的说明。")}</div>
                <ul className="wa-list" data-testid="schedule-pending">
                  {result.pending.map((p, i) => (
                    <li key={i}><span className="tx"><span className="lb">{p.summary}</span>{p.detail && <span className="ds">{p.detail}</span>}</span></li>
                  ))}
                </ul>
                <div className="peernote">{t("这些都没有被执行，也没有被代答。")}</div>
              </>
            )}
            <span className="acts"><button className="act ghost" data-action="schedule.result-close" onClick={() => setResult(null)}>{t("关闭")}</button></span>
          </div>
        )}

        {!form && (
          <div className="peerform">
            <span className="acts">
              <button className="act" data-action="schedule.new" disabled={locked || board.schedules.length >= board.policy.maxSchedules}
                onClick={() => { setError(""); setPreview(null); setForm({ label: "", everySec: 86400, workspace: "", prompt: "" }); }}>{t("新建定时运行")}</button>
            </span>
          </div>
        )}

        {form && !preview && (
          <div className="peerform" role="group" aria-label={t("新建定时运行")}>
            <label className="peernote" htmlFor="sched-folder">{t("文件夹（它只能读这里）")}</label>
            {board.current?.trustable && (
              <div className="peernote">
                {board.current.trust === "declined"
                  ? t("当前项目「{name}」之前选了“暂不信任”，所以定时运行读不了它。信任后，在有系统沙盒的电脑上，默认设置下它的编辑和命令不再逐条询问；你手动选定的批准模式不受影响。", { name: board.current.name })
                  : t("当前项目「{name}」还没有受信任，所以定时运行读不了它。信任后，在有系统沙盒的电脑上，默认设置下它的编辑和命令不再逐条询问；你手动选定的批准模式不受影响。", { name: board.current.name })}
                {" "}
                <button className="act" data-action="workspace.trust" data-value="trusted" disabled={busy} onClick={() => void run(() => port.decideWorkspaceTrust("trusted"))}>{t("信任此文件夹")}</button>
              </div>
            )}
            {board.folders.length === 0 ? (
              !board.current?.trustable && <div className="peernote" id="sched-folder">{t("还没有受信任的项目。先在 Studio 或终端界面里打开一个项目并信任它，再回来选。")}</div>
            ) : (
              <select id="sched-folder" className="peerfield" data-action-change="schedule.folder" value={form.workspace} disabled={busy}
                onChange={(e) => draft({ workspace: e.target.value })}>
                <option value="">{t("选择一个受信任的项目")}</option>
                {board.folders.map((f) => <option key={f.path} value={f.path}>{f.name} — {f.path}</option>)}
              </select>
            )}
            <label className="peernote" htmlFor="sched-prompt">{t("要做什么")}</label>
            <textarea id="sched-prompt" className="peerfield" data-action-change="schedule.prompt" value={form.prompt} disabled={busy}
              onChange={(e) => draft({ prompt: e.target.value })} />
            <label className="peernote" htmlFor="sched-label">{t("名称（可选）")}</label>
            <input id="sched-label" className="peerfield" data-action-change="schedule.label" value={form.label} disabled={busy} onChange={(e) => draft({ label: e.target.value })} />
            <label className="peernote" htmlFor="sched-every">{t("多久一次")}</label>
            <select id="sched-every" className="peerfield" data-action-change="schedule.every" value={form.everySec ?? 86400} disabled={busy}
              onChange={(e) => draft({ everySec: Number(e.target.value) })}>
              {EVERY.map(([sec, label]) => <option key={sec} value={sec} disabled={sec < board.policy.minIntervalMinutes * 60}>{t(label)}</option>)}
            </select>
            <span className="acts">
              <button className="act ghost" data-action="schedule.cancel" disabled={busy} onClick={() => setForm(null)}>{t("取消")}</button>
              <button className="act" data-action="schedule.preview" data-primary="" disabled={busy || !form.workspace.trim() || !form.prompt.trim()}
                onClick={() => void run(async () => setPreview(await port.previewSchedule({ ...form, workspace: form.workspace.trim(), prompt: form.prompt.trim() })))}>{t("预览")}</button>
            </span>
          </div>
        )}

        {form && preview && (
          <div className="peerform" role="group" aria-label={t("确认创建")}>
            <div className="peernote"><b>{t("请确认")}</b></div>
            <ul className="wa-list">
              <li><span className="tx"><span className="lb">{t("第一次运行")}</span><span className="ds">{when(preview.firstRunUnix)}</span></span></li>
              <li><span className="tx"><span className="lb">{t("文件夹")}</span><span className="ds">{preview.workspace} {t("（只读：它能读这里的文件，不写任何东西）")}</span></span></li>
              <li><span className="tx"><span className="lb">{t("模型")}</span><span className="ds">{preview.model}</span></span></li>
              <li><span className="tx"><span className="lb">{t("要做什么")}</span><span className="ds">{preview.prompt}</span></span></li>
              <li><span className="tx"><span className="lb">{t("每次最多")}</span><span className="ds">{t("{a} 令牌，{b} 步，{c} 秒", { a: preview.perRunTokens.toLocaleString(), b: preview.perRunSteps, c: preview.perRunWallSec })}</span></span></li>
              <li><span className="tx"><span className="lb">{t("总共最多")}</span><span className="ds">{t("每天 {a} 令牌，一共 {b} 令牌，{d} 自动到期", { a: preview.perDayMax.toLocaleString(), b: preview.lifetimeTokens.toLocaleString(), d: when(preview.expiresAtUnix) })}</span></span></li>
            </ul>
            <div className="peernote">{t("需要你批准的事会挂起等你，不会替你答复。")}</div>
            <span className="acts">
              <button className="act ghost" data-action="schedule.back" disabled={busy} onClick={() => setPreview(null)}>{t("返回修改")}</button>
              <button className="act" data-action="schedule.create" data-primary="" disabled={locked}
                onClick={() => void run(async () => { await port.createSchedule({ ...form, workspace: form.workspace.trim(), prompt: form.prompt.trim() }, preview.confirm); setForm(null); setPreview(null); })}>{t("确认创建")}</button>
            </span>
          </div>
        )}

        {readOnly && <div className="peernote" role="status">{t("这些设置现在是只读的：这个窗口没有被授予编辑权限。")}</div>}
        {error && <div className="why" role="alert">{error}</div>}
      </div>
    </Group>
  );
}
