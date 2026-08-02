import { useState } from "react";
import { KeyRound } from "lucide-react";
import type { AppBindings } from "../lib/bridge";
import type { Translator } from "../lib/i18n";
import type { SessionRuntimeIssue } from "../lib/types";

// The locale dictionary is a closed key union; the owner/action kind strings
// come from the backend, so translate them through a dynamic-key helper.
const translateDynamic = (t: Translator, key: string): string =>
  t(key as Parameters<Translator>[0]);

interface Props {
  issue: SessionRuntimeIssue;
  tabID: string;
  t: Translator;
  api: Pick<AppBindings, "ResolveSessionRuntimeIssue">;
}

const ownerKindKey: Record<string, string> = {
  current_tab: "session.ownerCurrentTab",
  current_detached: "session.ownerCurrentDetached",
  same_instance_hidden: "session.ownerSameHidden",
  external_process: "session.ownerExternal",
  stale_reclaimed: "session.ownerStale",
  unknown: "session.ownerUnknown",
};

const actionKey: Record<string, string> = {
  focus: "session.actionFocus",
  retry: "session.actionRetry",
  read_only: "session.actionReadOnly",
  copy: "session.actionCopy",
};

/**
 * Session ownership issue card: replaces the opaque "session already open"
 * error with the classified owner (another tab, a background task, a hidden
 * window, an external process, or a stale lock) and the allowed actions.
 */
export function SessionIssueCard({ issue, tabID, t, api }: Props) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  if (done || !issue.actions?.length) {
    return null;
  }
  const ownerLabel = translateDynamic(t, ownerKindKey[issue.ownerKind ?? "unknown"] ?? "session.ownerUnknown");

  const run = (action: string) => {
    setBusy(true);
    api.ResolveSessionRuntimeIssue(tabID, issue.issueId ?? "", action)
      .then(() => setDone(true))
      .catch(() => setBusy(false));
  };

  return (
    <div className="banner banner--warning banner--actionable" role="status">
      <KeyRound size={14} aria-hidden />
      <span className="banner__msg">
        {translateDynamic(t, "session.issueCard") !== "session.issueCard" ? translateDynamic(t, "session.issueCard").replace("{owner}", ownerLabel) : ownerLabel}
        <span className="banner__sub">{issue.message}</span>
      </span>
      <span className="banner__spacer" />
      {issue.actions.map((action) => (
        <button
          key={action}
          type="button"
          className="btn btn--small"
          disabled={busy}
          onClick={() => run(action)}
        >
          {translateDynamic(t, actionKey[action] ?? "session.actionRetry")}
        </button>
      ))}
    </div>
  );
}
