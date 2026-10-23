// Scheduled runs: read-only tasks a person confirmed, run at a time they chose.
// Times are Unix seconds. A report is the model's own text: it is labelled as
// such and drawn without loading remote content.

export interface ScheduleDraft {
  label: string;
  everySec?: number;
  atUnix?: number;
  workspace: string;
  prompt: string;
  model?: string;
}

export interface ScheduleView {
  id: string;
  label: string;
  status: string;
  pausedReason?: string;
  everySec?: number;
  atUnix?: number;
  workspace: string;
  model: string;
  prompt: string;
  perRunTokens: number;
  lifetimeTokens: number;
  spentLifetimeTokens: number;
  consecutiveFailures?: number;
  expiresAtUnix: number;
}

export interface ScheduleRunView {
  triggerId: string;
  scheduleId: string;
  slotUnix: number;
  state: string;
  skipReason?: string;
  chargedTokens: number;
  endedUnix?: number;
}

export interface SchedulePolicyView {
  minIntervalMinutes: number;
  maxSchedules: number;
  globalTokensDay: number;
  expiryDays: number;
}

export interface ScheduleBoard {
  pausedAll: boolean;
  pausedReason?: string;
  schedules: ScheduleView[];
  runs: ScheduleRunView[];
  policy: SchedulePolicyView;
  // The trusted projects a schedule may be given. A creation names one of these
  // and nothing else; the kernel refuses any other path.
  folders: ScheduleFolder[];
  // The open project when it is not one of those yet, with the recorded answer
  // about it ("" undecided, "declined" after a refusal).
  current?: ScheduleCurrent;
}

export interface ScheduleCurrent {
  path: string;
  name: string;
  trust: string;
  trustable: boolean;
}

export interface ScheduleFolder {
  path: string;
  name: string;
}

export interface SchedulePreview {
  firstRunUnix: number;
  workspace: string;
  model: string;
  prompt: string;
  perRunTokens: number;
  perRunSteps: number;
  perRunWallSec: number;
  perDayMax: number;
  lifetimeTokens: number;
  expiresAtUnix: number;
  confirm: string;
}

export interface SchedulePendingView {
  kind: string;
  summary: string;
  detail?: string;
}

export interface ScheduleResultView {
  triggerId: string;
  report: string;
  reportTruncated?: boolean;
  pending: SchedulePendingView[];
  pendingDropped?: number;
  enforcement: string;
  recordedUnix: number;
}
