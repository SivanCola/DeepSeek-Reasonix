import { useEffect, useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort, MarketPackage, MarketPlan } from "../port/port";
import { Outcome } from "./AddPlugin";
import { PlanConfirm } from "./MarketConfirm";

interface Props {
  port: AgentPort;
  pkg: MarketPackage;
  onBack: () => void;
  onInstalled: () => void;
}

// A publisher installing their own package: the kernel fetches it from the
// registry as the account, previews it, and installs only what matches that
// preview's digest. This view never holds a source of its own to send.
export function OwnInstall({ port, pkg, onBack, onInstalled }: Props) {
  const [plan, setPlan] = useState<MarketPlan | null>(null);
  const [done, setDone] = useState<MarketPlan | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const replace = !!pkg.installed && pkg.installed.version !== pkg.latestVersion;

  useEffect(() => {
    let live = true;
    port
      .planOwnMarket({ slug: pkg.slug, replace })
      .then((p) => live && setPlan(p))
      .catch((e) => live && setError(reason(e)))
      .finally(() => live && setBusy(false));
    return () => {
      live = false;
    };
  }, [port, pkg.slug, replace]);

  const install = async () => {
    if (!plan) return;
    setBusy(true);
    setError("");
    try {
      const out = await port.installOwnMarket({
        slug: pkg.slug, version: plan.version, planId: plan.planId, replace, digest: plan.contentDigest,
      });
      setDone(out);
      if (out.applied) onInstalled();
    } catch (e) {
      setError(reason(e));
    } finally {
      setBusy(false);
    }
  };

  const back = (
    <button className="act" data-action="market.back" onClick={onBack}>
      {t("返回我的发布")}
    </button>
  );

  if (done) {
    return (
      <div className="mkt addpkg" data-stage="done">
        <Outcome plan={done} />
        <div className="acts">{back}</div>
      </div>
    );
  }
  if (plan) {
    return <PlanConfirm key={plan.planId} slug={pkg.slug} plan={plan} busy={busy} error={error} onCancel={onBack} onInstall={() => void install()} own />;
  }
  return (
    <div className="mkt">
      {error ? (
        <div className="find" data-lvl="err">
          <span className="t">{t("无法预览 {name}", { name: pkg.slug })}</span>
          <span className="why">{error}</span>
        </div>
      ) : (
        <div className="empty">{t("正在预览将安装的内容…")}</div>
      )}
      <div className="acts">{back}</div>
    </div>
  );
}
