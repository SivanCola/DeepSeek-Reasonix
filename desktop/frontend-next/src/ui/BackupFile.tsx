import { useCallback, useEffect, useRef, useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort, BackupCategory, BackupFileCatalog } from "../port/port";
import { BackupRestore, CATEGORY_LABEL, type RestoreSource } from "./BackupRestore";
import { CATEGORY_NOTE } from "./Backup";
import { HttpError } from "../port/http_error";

// The same sealed envelope the account keeps, written to a file the person
// chooses. It asks nothing of the account: the passphrase is the only secret,
// and the kernel offers it to this window alone.
export function BackupFile({ port }: { port: AgentPort }) {
  const [connection, setConnection] = useState({ port, generation: 0 });
  if (connection.port !== port) setConnection({ port, generation: connection.generation + 1 });
  return <BackupFileInput key={connection.generation} port={port} />;
}

function BackupFileInput({ port }: { port: AgentPort }) {
  const [catalog, setCatalog] = useState<BackupFileCatalog | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [readError, setReadError] = useState("");
  const [chosen, setChosen] = useState<Set<BackupCategory>>(new Set());
  const [pass, setPass] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [source, setSource] = useState<RestoreSource | null>(null);
  const chooser = useRef<HTMLInputElement>(null);
  const live = useRef(false);
  const reading = useRef<object | null>(null);

  const load = useCallback(async () => {
    const request = {};
    reading.current = request;
    setReadError("");
    setUnavailable(false);
    try {
      const c = await port.backupFileCatalog();
      if (reading.current !== request) return;
      setCatalog(c);
      setChosen(new Set(c.categories.filter((x) => x.defaultOn).map((x) => x.id)));
    } catch (e) {
      if (reading.current !== request) return;
      if (e instanceof HttpError && e.reason?.code === "device.host_only") setUnavailable(true);
      else setReadError(reason(e));
    }
  }, [port]);
  useEffect(() => {
    live.current = true;
    void load();
    return () => { live.current = false; reading.current = null; };
  }, [load]);

  if (unavailable) return <p className="acct-note">{t("配置文件的导出与导入只在本机窗口可用。")}</p>;
  if (readError) return <div className="find" data-lvl="err" role="alert">
    <span className="t">{t("无法读取备份配置")}</span>
    <span className="why">{readError}</span>
    <button className="act" data-action="backup.file-reload" onClick={() => void load()}>{t("重试")}</button>
  </div>;
  if (!catalog) return <p className="acct-note">{t("正在读取…")}</p>;

  const min = catalog.minPassphrase;
  const mismatch = again !== "" && pass !== again;
  const ready = chosen.size > 0 && pass.length >= min && pass === again && !busy;
  const wait = !chosen.size ? t("至少选择一项备份内容")
    : pass.length < min ? t("口令还差 {n} 个字符", { n: min - pass.length })
      : again === "" ? t("请再输一次口令") : "";

  const toggle = (id: BackupCategory) =>
    setChosen((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const exportFile = async () => {
    setBusy(true);
    setNote("");
    setError("");
    try {
      const order = catalog.categories.map((c) => c.id).filter((id) => chosen.has(id));
      const out = await port.exportBackupFile(order, pass);
      if (!live.current) return;
      await port.saveText(out.name, out.file);
      if (!live.current) return;
      setPass("");
      setAgain("");
      const skipped = out.omitted?.length ?? 0;
      setNote(skipped ? t("已导出。{n} 个文件过大或无法读取，未包含在内。", { n: skipped }) : t("已导出。"));
    } catch (e) {
      setError(reason(e));
    } finally {
      setBusy(false);
    }
  };

  const choose = async (file: File | undefined) => {
    if (!file) return;
    setError("");
    try {
      const text = (await file.text()).trim();
      if (!live.current) return;
      setSource({
        name: file.name,
        preview: (passphrase) => port.previewBackupFile(text, passphrase),
        apply: (planId, items, consented) => port.applyBackupFile(planId, items, consented),
        // A file from elsewhere starts by adding what this machine lacks; what it
        // would replace is the person's to tick.
        preselect: (item) => item.recommended && item.status === "new",
      });
    } catch (e) {
      setError(reason(e));
    }
  };

  return (
    <div className="bk">
      <p className="acct-note">
        {t("导出的文件用你设置的口令在本机加密，不含明文。忘记口令就无法打开；密钥只有在勾选后才会写进文件。")}
      </p>
      <div className="bk-cats" role="group" aria-label={t("导出内容")}>
        {catalog.categories.map((c) => (
          <label key={c.id} className="bk-cat" data-warn={c.id === "secrets" && chosen.has(c.id) ? "" : undefined}>
            <input type="checkbox" checked={chosen.has(c.id)} disabled={busy} onChange={() => toggle(c.id)} data-action="backup.category" data-target={c.id} />
            <span className="nm">{t(CATEGORY_LABEL[c.id])}</span>
            <span className="why">{t(c.id === "secrets" ? "默认不导出。勾选后密钥会随文件一起加密，拿到文件和口令的人就能使用它们" : CATEGORY_NOTE[c.id])}</span>
          </label>
        ))}
      </div>
      <div className="bk-fields">
        <label className="grow">
          <span>{t("加密口令（至少 {n} 个字符）", { n: min })}</span>
          <input data-action="backup.passphrase" data-target="new" type="password" autoComplete="new-password" value={pass} disabled={busy} onChange={(e) => setPass(e.target.value)} />
        </label>
        <label className="grow">
          <span>{t("再输一次")}</span>
          <input data-action="backup.passphrase" data-target="again" type="password" autoComplete="new-password" value={again} disabled={busy} onChange={(e) => setAgain(e.target.value)} />
        </label>
      </div>
      {mismatch && <p className="acct-note" data-err="">{t("两次输入的口令不一致")}</p>}
      <div className="bk-acts">
        <button className="act" data-primary data-action="backup.file-export" disabled={!ready} aria-describedby={!ready && wait ? "bkf-wait" : undefined} onClick={() => void exportFile()}>
          {t(busy ? "正在加密…" : "导出到文件…")}
        </button>
        {!ready && !busy && wait && <span id="bkf-wait" className="acct-note" aria-live="polite" data-action="backup.wait">{wait}</span>}
        <button className="act" data-action="backup.file-import" disabled={busy} onClick={() => chooser.current?.click()}>{t("从文件导入…")}</button>
        <input ref={chooser} type="file" accept=".rxcb,text/plain" hidden data-action="backup.file-choose" onChange={(e) => { void choose(e.target.files?.[0]); e.target.value = ""; }} />
      </div>
      {note && <p className="acct-note">{note}</p>}
      {error && <p className="acct-note" data-err="">{error}</p>}
      {source && <BackupRestore key={source.name} port={port} source={source} onClose={() => setSource(null)} />}
    </div>
  );
}
