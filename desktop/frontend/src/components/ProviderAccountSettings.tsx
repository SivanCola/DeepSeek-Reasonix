import { useState } from "react";
import { asArray } from "../lib/array";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { ProviderAccountView as AccountView } from "../lib/types";

export function normalizeProviderAccountView(p: AccountView): AccountView {
  return {
    ...p,
    providerId: String(p.providerId ?? ""),
    accountId: String(p.accountId ?? ""),
    label: String(p.label ?? ""),
    apiKeyEnv: String(p.apiKeyEnv ?? ""),
    enabled: p.enabled !== false,
    default: Boolean(p.default),
    keySet: Boolean(p.keySet),
    providerNames: asArray(p.providerNames),
  };
}

export function accountsForProviderGroup(group: { id: string; providers: { providerId?: string }[] }, accounts: AccountView[]): AccountView[] {
  const ids = new Set(group.providers.map((p) => p.providerId).filter(Boolean) as string[]);
  if (group.id === "builtin:deepseek") ids.add("deepseek");
  if (group.id === "custom:opencode-go") ids.add("opencode-go");
  return accounts.filter((account) => ids.has(account.providerId) && !account.retired);
}

export function addAccountPresetID(group: { id: string }): string {
  if (group.id === "builtin:deepseek" || group.id.endsWith(":deepseek")) return "deepseek-anthropic";
  if (group.id === "custom:opencode-go") return "opencode-go-recommended";
  return "";
}

export function ProviderAccountManager({
  group,
  accounts,
  busy,
  apply,
}: {
  group: { id: string };
  accounts: AccountView[];
  busy: boolean;
  apply: (fn: () => Promise<unknown>) => Promise<unknown>;
}) {
  const t = useT();
  const [adding, setAdding] = useState(false);
  const [label, setLabel] = useState("");
  const [key, setKey] = useState("");
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renameLabel, setRenameLabel] = useState("");
  const presetID = addAccountPresetID(group);
  if (accounts.length === 0 && !presetID) return null;

  return (
    <div className="provider-accounts">
      <div className="provider-card-block__label">{t("settings.providerAccounts")}</div>
      {accounts.map((account) => (
        <div key={`${account.providerId}/${account.accountId}`} className="provider-account-row">
          {renaming === account.accountId ? (
            <>
              <input
                className="input"
                value={renameLabel}
                onChange={(e) => setRenameLabel(e.target.value)}
                aria-label={t("settings.accountLabel")}
              />
              <button
                type="button"
                className="btn btn--small"
                disabled={busy || !renameLabel.trim()}
                onClick={() => apply(() => app.RenameProviderAccount(account.providerId, account.accountId, renameLabel.trim())).then(() => setRenaming(null))}
              >
                {t("common.save")}
              </button>
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => setRenaming(null)}>{t("common.cancel")}</button>
            </>
          ) : (
            <>
              <strong>{account.label}</strong>
              {account.default ? <span className="badge">{t("settings.accountDefault")}</span> : null}
              <span>{account.enabled ? t("settings.accountEnabled") : t("settings.accountDisabled")}</span>
              <span>{account.keySet ? t("settings.keySet") : t("settings.noKey")}</span>
              <button type="button" className="btn btn--small" disabled={busy || account.default} onClick={() => apply(() => app.SetProviderAccountDefault(account.providerId, account.accountId))}>
                {t("settings.accountSetDefault")}
              </button>
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => apply(() => app.SetProviderAccountEnabled(account.providerId, account.accountId, !account.enabled))}>
                {account.enabled ? t("settings.accountDisable") : t("settings.accountEnable")}
              </button>
              <button
                type="button"
                className="btn btn--small"
                disabled={busy}
                onClick={() => {
                  setRenaming(account.accountId);
                  setRenameLabel(account.label);
                }}
              >
                {t("common.edit")}
              </button>
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => apply(() => app.RetireProviderAccount(account.providerId, account.accountId))}>
                {t("settings.accountRetire")}
              </button>
            </>
          )}
        </div>
      ))}
      {presetID && adding && (
        <div className="provider-account-add">
          <input className="input" value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("settings.accountLabel")} />
          <input className="input" type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder={t("settings.accountApiKey")} />
          <button
            type="button"
            className="btn btn--small"
            disabled={busy || !label.trim() || !key.trim()}
            onClick={() => apply(() => app.AddProviderPresetAccount(presetID, label.trim(), key)).then(() => {
              setAdding(false);
              setLabel("");
              setKey("");
            })}
          >
            {t("common.save")}
          </button>
          <button type="button" className="btn btn--small" disabled={busy} onClick={() => setAdding(false)}>{t("common.cancel")}</button>
        </div>
      )}
      {presetID && !adding && (
        <button type="button" className="btn btn--small" disabled={busy} onClick={() => setAdding(true)}>
          {t("settings.addAccount")}
        </button>
      )}
    </div>
  );
}
