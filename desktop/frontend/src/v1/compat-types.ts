export type UsageStats = {
  totalCostUsd: number;
  totalPromptTokens: number;
  totalCompletionTokens: number;
  cacheHitTokens: number;
  cacheMissTokens: number;
  lastCallCacheHit: number | null;
  lastCallCacheMiss: number | null;
  reservedTokens: number;
  liveLogTokens: number;
};

export type AssistantSegment =
  | { kind: "text"; text: string }
  | { kind: "reasoning"; text: string }
  | {
      kind: "tool";
      callId: string;
      name: string;
      args: string;
      startedAt: number;
      result?: string;
      ok?: boolean;
      durationMs?: number;
    };

export type SkillOrigin = {
  name: string;
  runAs: "inline" | "subagent";
};

export type ApprovalPrompt = {
  kind: string;
  tone: "ok" | "warn" | "error" | "accent" | "info" | "ghost";
  title: string;
  subtitle?: string;
  preview?: string;
  data?: Record<string, unknown>;
  meta?: Record<string, unknown>;
  actions: Array<{ kind: string; label: string }>;
};

export type PendingConfirm = {
  id: number;
  kind: "run_command" | "run_background";
  command: string;
  prompt: ApprovalPrompt;
};

export type PendingChoice = {
  id: number;
  question: string;
  options: { id: string; title: string; summary?: string }[];
  allowCustom: boolean;
};

export type PlanStep = {
  id: string;
  title: string;
  action: string;
  risk?: "low" | "med" | "high";
};

export type PendingPlan = {
  id: number;
  plan: string;
  summary?: string;
  steps?: PlanStep[];
};

export type ActivePlan = {
  plan: string;
  summary?: string;
  steps: PlanStep[];
  completedStepIds: string[];
  stepResults: Record<string, string>;
};

export type PendingCheckpoint = {
  id: number;
  stepId: string;
  title?: string;
  result: string;
  notes?: string;
  completed: number;
  total: number;
};

export type PendingRevision = {
  id: number;
  reason: string;
  remainingSteps: PlanStep[];
  summary?: string;
};

export type SessionInfo = {
  name: string;
  messageCount: number;
  mtime: string;
  summary?: string;
  workspaceStatus?: "matched" | "legacy_missing_meta";
};

export type SessionFile = {
  path: string;
  status: "c" | "m";
  lastSeenTurn?: number;
  firstSeenTurn?: number;
  added?: number;
  removed?: number;
};

export type Settings = {
  reasoningEffort: "low" | "medium" | "high" | "max";
  editMode: "review" | "auto" | "yolo" | "plan";
  budgetUsd: number | null;
  baseUrl?: string;
  apiKeyPrefix?: string;
  workspaceDir: string;
  recentWorkspaces: string[];
  model: string;
  editor?: string;
  desktopCloseBehavior?: "closeToTray" | "closeToQuit";
  webSearchEngine?:
    | "bing"
    | "bing-intl"
    | "searxng"
    | "metaso"
    | "baidu"
    | "tavily"
    | "perplexity"
    | "exa"
    | "brave"
    | "ollama";
  webSearchEndpoint?: string;
  webSearchApiKeys?: Record<string, string | undefined>;
  subagentModels?: Record<string, "flash" | "pro">;
  contextTokens?: Record<string, number>;
  showSystemEvents?: boolean;
  version: string;
};

export type BalanceInfoItem = {
  currency: string;
  total: number;
  granted?: number;
  toppedUp?: number;
};

export type Balance = {
  currency: string;
  total: number;
  isAvailable: boolean;
  infos: BalanceInfoItem[];
};
