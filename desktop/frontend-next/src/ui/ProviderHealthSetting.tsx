import { useEffect, useRef, useState } from "react";
import type { AgentPort } from "../port/port";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import { Switch } from "./Switch";
import { Group } from "./Group";

export function ProviderHealthSetting({ port, onChanged, onFailed }: {
  port: Partial<Pick<AgentPort, "providerHealthSettings" | "saveProviderHealthSettings">>;
  onChanged: () => void; onFailed: (why: string) => void;
}) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(() => {
    let live = true;
    generation.current++;
    setEnabled(null);
    setBusy(false);
    void port.providerHealthSettings?.().then((value) => { if (live) setEnabled(value.enabled); }).catch(() => {});
    return () => { live = false; generation.current++; };
  }, [port]);
  return <Group id="provider-health" title={t("自动检测连接与余额")}
    hint={t("默认关闭。开启后，打开模型列表会使用已保存密钥检查已授权的全局连接和余额，不发送生成请求。")}
    action={<Switch data-action="provider.health-setting" on={enabled === true}
      busy={busy || enabled === null || !port.saveProviderHealthSettings}
      label={t("自动检测连接与余额")} onClick={async () => {
        if (!port.saveProviderHealthSettings) return;
        const version = generation.current;
        setBusy(true);
        try {
          const value = await port.saveProviderHealthSettings(!enabled);
          if (version === generation.current) { setEnabled(value.enabled); onChanged(); }
        } catch (e) { if (version === generation.current) onFailed(reason(e)); }
        finally { if (version === generation.current) setBusy(false); }
      }} />} />;
}
