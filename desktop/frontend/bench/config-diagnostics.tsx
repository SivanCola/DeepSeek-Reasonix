import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { ConfigCompatibilitySettings, ConfigDiagnosticBanner } from "../src/components/ConfigDiagnostics";
import { LocaleProvider, useI18n } from "../src/lib/i18n";
import { installDesktopHostStub } from "../src/__tests__/desktopHostStub";
import type { ConfigDiagnosticSnapshot } from "../src/lib/configDiagnostics";
import "../src/styles.css";

let revision = 0;
const opened: string[] = [], clipboard: string[] = [];
function snapshot(tab: string, details = false): ConfigDiagnosticSnapshot {
  return { hostId: tab === "remote" ? "old-host" : "local", workspaceId: tab, workspaceRoot: `/fixture/${tab}`, instanceId: "browser-fixture", revision: ++revision,
    status: tab === "remote" ? "unsupported" : "ready", items: tab === "legacy" ? [{ id: "legacy-rules", code: "user_only", scope: "project", source: "/fixture/very-long-project-path/".repeat(8) + "reasonix.toml", field: "permissions.allow", severity: "info", status: "on_demand", summary: "On-demand approval", action: "view_settings", count: 39,
      ...(details ? { values: Array.from({ length: 39 }, (_, i) => `Bash=echo example-${i}\n${"long-command-".repeat(32)}`) } : {}) }] : tab === "broken" ? [{ id: "broken-config", code: "project_config_invalid", scope: "project", source: "/fixture/broken/reasonix.toml", field: "", severity: "warning", status: "needs_attention", summary: "Project configuration could not be read.", action: "open_config", count: 1 }] : [] };
}
const host = installDesktopHostStub({
  ConfigDiagnostics: async (tab: string) => snapshot(tab),
  ConfigDiagnosticDetails: async (tab: string) => snapshot(tab, true),
  OpenConfigDiagnosticSource: async (_tab: string, id: string) => { opened.push(id); },
}, { clipboardWrites: clipboard });
Object.assign(window, { configDiagnosticFixture: { opened, clipboard, host } });
function Fixture() {
  const [tab, setTab] = useState("legacy");
  const { setPref } = useI18n();
  return <main style={{ padding: 20, maxWidth: 900, margin: "auto", height: "100vh", overflow: "auto" }}>
    <nav>{["legacy", "clean", "broken", "remote"].map(id => <button className="btn" key={id} onClick={() => setTab(id)}>{id}</button>)}
      {(["en", "zh", "zh-TW"] as const).map(locale => <button className="btn" key={locale} onClick={() => setPref(locale)}>{locale}</button>)}
    </nav>
    <ConfigDiagnosticBanner tabId={tab} bindingKey={tab} />
    <ConfigCompatibilitySettings workspaceKey={`${tab}\u0000/fixture/${tab}`} />
  </main>;
}
createRoot(document.getElementById("root")!).render(<LocaleProvider><Fixture /></LocaleProvider>);
