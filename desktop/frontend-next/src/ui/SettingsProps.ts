import type { AccountState, AgentPort, Appearance as Look, SessionStatus } from "../port/port";
import type { HubPort } from "../port/hub";

export interface SettingsProps {
  // The window's own port, not a pane's: remote machines belong to the window,
  // and their endpoints sit above every pane's prefix.
  hub: HubPort;
  onError: (e: unknown) => void;
  port: AgentPort;
  // Machine-scoped pages use the window owner; session and project controls
  // continue to use the active pane.
  networkPort?: AgentPort;
  localWorkspacePort?: AgentPort;
  networkHost?: string;
  status: SessionStatus | null;
  theme: string;
  reloadThemes: () => void;
  onTheme: (t: string) => void;
  contrast: string;
  weight: string;
  onWeight: (v: string) => void;
  look: Look;
  onLook: (look: Look) => void;
  onContrast: (c: string) => void;
  onClose: () => void;
  onChanged: () => void;
  onSessionsRecovered?: () => void;
  at?: string;
  account: AccountState | null;
  accountUnread: string;
  reloadAccount: () => void;
  workspaceRoot?: string;
}
