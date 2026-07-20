import { useRef } from "react";
import { createPortal } from "react-dom";

import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useRemoteStore } from "../store/remote";

/**
 * Confirms that a remote SSH host may consume local model quota through the
 * Provider Broker. API keys never leave the local machine; this dialog only
 * authorizes provider refs for the bound host key fingerprint.
 */
export function RemoteProviderTrustDialog() {
  const t = useT();
  const prompt = useRemoteStore((s) => s.pendingProviderTrust);
  const clear = useRemoteStore((s) => s.clearPendingProviderTrust);
  const resolvingRef = useRef(false);

  if (!prompt) return null;

  const resolve = async (accept: boolean) => {
    if (resolvingRef.current) return;
    resolvingRef.current = true;
    try {
      await app.ConfirmRemoteProviderTrust(prompt.hostId, accept);
    } finally {
      clear(prompt);
      resolvingRef.current = false;
    }
  };

  return createPortal(
    <div className="remote-hostkey-overlay" role="dialog" aria-modal="true" aria-labelledby="remote-provider-trust-title">
      <div className="remote-hostkey-dialog">
        <h2 id="remote-provider-trust-title" className="remote-hostkey-dialog__title">
          {t("remote.providerTrust.title")}
        </h2>
        <p>{t("remote.providerTrust.body", { host: prompt.host || prompt.hostId })}</p>
        <p className="remote-provider-trust__workspace">
          {t("remote.providerTrust.workspace", { workspace: prompt.workspace })}
        </p>
        <p className="remote-provider-trust__fp">
          {prompt.keyType} {prompt.fingerprint}
        </p>
        <div className="remote-provider-trust__providers">
          <div className="remote-provider-trust__providers-label">{t("remote.providerTrust.providers")}</div>
          <ul>
            {(prompt.providerRefs ?? []).map((ref) => (
              <li key={ref}>
                <code>{ref}</code>
              </li>
            ))}
          </ul>
        </div>
        <p className="remote-provider-trust__warning">{prompt.warning || t("remote.providerTrust.warning")}</p>
        <div className="remote-hostkey-dialog__actions">
          <button type="button" className="btn" onClick={() => void resolve(false)}>
            {t("remote.providerTrust.decline")}
          </button>
          <button type="button" className="btn btn--primary" onClick={() => void resolve(true)}>
            {t("remote.providerTrust.authorize")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
