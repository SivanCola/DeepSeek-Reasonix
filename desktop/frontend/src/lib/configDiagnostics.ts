export interface ConfigDiagnostic {
  id: string;
  code: string;
  scope: "user" | "project";
  source: string;
  field: string;
  severity: "info" | "warning" | "error";
  status: "on_demand" | "needs_attention" | "awaiting_approval";
  summary: string;
  action: string;
  count: number;
  values?: string[];
}

export interface ConfigDiagnosticSnapshot {
  hostId: string;
  workspaceId: string;
  workspaceRoot: string;
  instanceId: string;
  revision: number;
  status: "ready" | "unavailable" | "unsupported";
  items: ConfigDiagnostic[];
}

export function normalizeConfigDiagnostics(value: unknown): ConfigDiagnosticSnapshot | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Partial<ConfigDiagnosticSnapshot>;
  if (typeof raw.hostId !== "string" || typeof raw.workspaceId !== "string" ||
      typeof raw.workspaceRoot !== "string" || typeof raw.instanceId !== "string" ||
      !Number.isSafeInteger(raw.revision) || (raw.revision ?? -1) < 0 ||
      !["ready", "unavailable", "unsupported"].includes(raw.status ?? "")) return null;
  const items = Array.isArray(raw.items) ? raw.items.filter((item): item is ConfigDiagnostic => Boolean(item) &&
    typeof item.id === "string" && typeof item.code === "string" && typeof item.source === "string" &&
    typeof item.field === "string" && typeof item.summary === "string" && typeof item.action === "string" &&
    ["user", "project"].includes(item.scope) && ["info", "warning", "error"].includes(item.severity) &&
    ["on_demand", "needs_attention", "awaiting_approval"].includes(item.status) && Number.isSafeInteger(item.count) && item.count > 0) : [];
  if (Array.isArray(raw.items) && items.length !== raw.items.length) return null;
  return { ...raw, items } as ConfigDiagnosticSnapshot;
}

export function diagnosticDismissKey(snapshot: ConfigDiagnosticSnapshot, item: ConfigDiagnostic): string {
  return JSON.stringify([snapshot.hostId, item.scope === "user" ? "user" : snapshot.workspaceId, item.id]);
}
