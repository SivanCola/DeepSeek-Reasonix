import { useLayoutEffect, useMemo, useState, type CSSProperties } from "react";
import { createRoot } from "react-dom/client";
import { ChatPaneRegion, type ChatPaneTranscriptInput } from "../src/app-shell/ChatPaneRegion";
import { DockLauncher } from "../src/components/DockLauncher";
import { initialState, type Item } from "../src/lib/useController";
import { applyConversationWidth } from "../src/lib/conversationWidth";
import { LocaleProvider, useT } from "../src/lib/i18n";
import { installDesktopHostStub } from "../src/__tests__/desktopHostStub";
import "../src/styles.css";

type Options = {
  layout: "creation" | "workbench";
  width: "standard" | "full";
  sidebar: boolean;
  dock: boolean;
  launcher: boolean;
  long: boolean;
  turns: number;
};
declare global {
  interface Window {
    transcriptLayoutFixture: {
      configure: (next: Partial<Options>) => void;
      revision: number;
    };
  }
}

// Only the backend boundary is stubbed: layout, launcher, projection, windowing
// and message/code rendering below are the production components and stylesheet.
installDesktopHostStub({
  ReportFrontendDiagnostic: async () => {},
  GitBranches: async () => [],
  WorkspaceChanges: async () => [],
  ToolResultForTab: async () => null,
});
const noAction = () => {};
const commands = {
  onPrompt: noAction, onDeliveryContinue: undefined, onAcceptDelivery: undefined,
  onOpenChanges: undefined, onOpenVerification: undefined, onEditPrompt: undefined,
  onRewind: undefined, onLoadOlderHistory: undefined, onSurfacePaintReady: undefined,
};
function Fixture() {
  const t = useT();
  const [options, setOptions] = useState<Options>({
    layout: "creation", width: "full", sidebar: true, dock: false, launcher: false, long: true, turns: 2,
  });
  const [revision, setRevision] = useState(0);
  const items = useMemo(() => Array.from({ length: options.turns }, (_, index): Item[] => [
    { kind: "user", id: "user-" + index, text: "USER MESSAGE MUST REMAIN VISIBLE " + index },
    { kind: "assistant", id: "answer-" + index, text: "\`\`\`text\n" + (options.long ? "abcdefghij".repeat(60) : "short") + "\n\`\`\`", reasoning: "", streaming: false },
  ]).flat(), [options.long, options.turns]);
  useLayoutEffect(() => {
    document.documentElement.dataset.themeStyle = "graphite";
    document.documentElement.dataset.theme = "light";
    document.documentElement.dataset.platform = "windows";
    applyConversationWidth(options.width);
    window.transcriptLayoutFixture = {
      configure: next => { setOptions(current => ({ ...current, ...next })); setRevision(value => value + 1); },
      revision,
    };
  }, [options.width, revision]);
  const transcript: ChatPaneTranscriptInput = {
    state: { ...initialState, items }, items, tabId: "layout-fixture", geometrySessionKey: "layout-fixture",
    footerHeight: 100, revealSignal: undefined, invocationMetadata: undefined, surfaceCommitToken: undefined,
    liveStore: undefined, transcriptHydrating: false, navigationDataReady: true, readOnly: false,
    controllerReady: true, hydratePlaceholderActive: false, clearContextPending: false,
    creation: options.layout === "creation", availability: { kind: "ready", source: "history" },
    rewind: { stateActive: false, committing: false, signal: undefined },
  };
  return <div className={"app app--windows app--windows-frameless app--" + options.layout} data-fixture-revision={revision}>
    <div className={`layout${options.sidebar ? "" : " layout--sidebar-collapsed"}${options.dock ? " layout--workspace-open" : ""}`}
      style={{ "--sidebar-expanded-width": options.layout === "creation" ? "236px" : "300px", "--workspace-width": "300px" } as CSSProperties}>
      <header className="topicbar">Transcript layout fixture</header>
      <aside className="sidebar">Sidebar</aside>
      <div className="chat-pane">
        <ChatPaneRegion transitioning={false} t={t} imDetail={null} remote={undefined} commands={commands}
          transcript={transcript} onRetryHistory={async () => {}}
          launcher={options.launcher ? <DockLauncher tabId="layout-fixture" scopeKey="layout-fixture"
            workspaceRoot="" visible onSelect={noAction} overlay={options.dock} /> : undefined} />
        <footer className="footer"><div className="composer-wrap"><textarea aria-label="Draft" defaultValue="Draft remains inside the chat pane" /></div></footer>
      </div>
      {options.dock && <aside className="workbench-dock" style={{ gridColumn: 3, gridRow: 2 }}>Workspace</aside>}
    </div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<LocaleProvider><Fixture /></LocaleProvider>);
