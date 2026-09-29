import { useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { saveModelSettings } from "../lib/modelSettings";
import type { WireEvent } from "../lib/types";
import { useAppNavigationStore } from "../store/appNavigation";
import { ErrorMessage } from "./ErrorMessage";

/** Only structured transport evidence can offer a connection policy change. */
export function ProviderHTTPCompatibilityActions({ diagnostic, tabId, hostId }: {
  diagnostic?: WireEvent["diagnostic"]; tabId?: string; hostId?: string;
}) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const inFlight = useRef(false);
  const providerId = diagnostic?.providerId;
  // Remote diagnostics can use remote-owned or virtual provider identities.
  // Never apply those identities to an unrelated local connection.
  if (diagnostic?.kind !== "transport_protocol" || diagnostic.transportCode !== "PROTOCOL_ERROR" || !providerId || (hostId && hostId !== "local")) return null;
  const openSettings = () => {
    const navigation = useAppNavigationStore.getState();
    navigation.setSettingsFocus(current => ({ target: "model-access", providerName: providerId, sourceTabId: tabId, requestId: (current?.requestId ?? 0) + 1 }));
    navigation.setSettingsTarget("providers");
  };
  const enable = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      const settings = await app.Settings();
      const provider = settings.providers.find(p => p.name === providerId);
      if (!provider) throw new Error(t("error.http1ConnectionMissing"));
      if (!provider.http1Only) await saveModelSettings(settings, { kind: "http1_compatibility", name: providerId, enabled: true });
      // Saving is not proof of runtime application. Existing turn admission
      // waits for the model settings owner to install the new client.
      setSaved(true);
    } catch (cause) { setError(cause); }
    finally { inFlight.current = false; setBusy(false); }
  };
  return <div className="provider-http-actions">
    {saved ? <span role="status">{t("error.http1Saved")}</span> : <>
      <button type="button" className="btn btn--small btn--primary" disabled={busy} onClick={() => void enable()}>{t(busy ? "error.http1Saving" : "error.enableHTTP1")}</button>
      <span className="mem-hint">{t("error.http1NoReplay")}</span>
    </>}
    <button type="button" className="btn btn--small" onClick={openSettings}>{t("error.connectionSettings")}</button>
    {error != null && <span role="alert"><ErrorMessage error={error} /></span>}
  </div>;
}
