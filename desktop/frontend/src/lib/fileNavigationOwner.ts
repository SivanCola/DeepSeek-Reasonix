import {
  sameAccessContext,
  type FileAccessContext,
  type FileResourceRef,
  type ResolvedFileResource,
} from "./fileResource";

/** Navigation parameters: how a resource is shown, never what identifies it. */
export type FileNavigationParams = Readonly<{
  action: "preview" | "source" | "reveal-tree";
  /** Dock surface the command targets: the file preview or the change list. */
  view: "files" | "changed";
}>;

export type FileNavigationAction = FileNavigationParams["action"];

/** What an open command produced, for the row or preview area that asked for it. */
export type FileNavigationOutcome =
  | Readonly<{ status: "opened"; resource: ResolvedFileResource }>
  | Readonly<{ status: "cancelled"; reason: "superseded" | "closed" | "disposed" | "unavailable" }>
  | Readonly<{ status: "failed"; error: Error }>;

/** One open preview: the confirmed resource plus the mode its tab renders in. */
export type FilePreviewEntry = Readonly<{ resource: ResolvedFileResource; source: boolean }>;

export type FileNavigationCommand = Readonly<{ ref: FileResourceRef; params: FileNavigationParams }>;

/** A dock instance: the session tab it belongs to and the dock tab that shows it. */
export type FileNavigationScope = Readonly<{ sessionTabId: string; dockTabId: string }>;

export const fileNavigationKey = (scope: FileNavigationScope): string =>
  `${scope.sessionTabId}\u0000${scope.dockTabId}`;

/** The last explicit navigation this record committed; a render never replays it. */
export type FileNavigationIntent = Readonly<{
  /** Advances per navigation command; a repeat of the same one keeps the value. */
  revision: number;
  resource: ResolvedFileResource;
  params: FileNavigationParams;
}>;

export type FileNavigationSnapshot = Readonly<{
  /** Workspace scope this dock currently shows; null until a panel binds one. */
  scopeKey: string | null;
  sessionTabId: string;
  dockTabId: string;
  /** Bumped whenever this dock's record is dropped and created again. */
  generation: number;
  /** Open preview entries, least recently used first. */
  entries: readonly FilePreviewEntry[];
  /** File-preview selection: the entry the preview area shows. */
  selected: FilePreviewEntry | null;
  /** Paths rendering as source, in entry order. */
  sourcePaths: readonly string[];
  /** Last explicit navigation command. */
  navigation: FileNavigationIntent | null;
  /** Display revision: bumped by every command that changed what is shown. */
  revision: number;
  /** Read-input revision: bumped only when the selected file's read inputs change. */
  contentRevision: number;
  /** Advances with every reveal-tree command so a repeat still expands the tree. */
  treeReveal: number;
  /** Aborted when this record's lifetime ends: dock closed, scope or runtime changed. */
  signal: AbortSignal;
}>;

export const FILE_PREVIEW_LIMIT = 5;

export type FileNavigationPorts = {
  /** Confirm a caller reference through the existing backend entry points. */
  resolve(ref: FileResourceRef): ResolvedFileResource | Promise<ResolvedFileResource>;
  /** Bring the dock that presents this resource forward; returns its dock tab id. */
  revealDock(ref: FileResourceRef): string;
};

export type FileNavigationRestore = Readonly<{
  paths: readonly string[];
  selectedPath: string | null;
  hostId: string;
}>;

/** A one-shot operation in a dock instance, e.g. creating a browser preview. */
export type FileNavigationOperation = Readonly<{
  signal: AbortSignal;
  /** True while no newer operation for the same dock has taken over. */
  owns(): boolean;
  finish(): void;
}>;

type Record = {
  snapshot: FileNavigationSnapshot;
  /** Lifetime of this dock instance's record; a reset aborts it. */
  lifetime: AbortController;
  /** Open command still resolving; a newer one supersedes it. */
  pending: AbortController | null;
  /** Counter behind `navigation.revision`, kept across a state-less repeat. */
  navigationRevision: number;
};

const SUPERSEDED: FileNavigationOutcome = { status: "cancelled", reason: "superseded" };
const asError = (reason: unknown): Error => (reason instanceof Error ? reason : new Error(String(reason)));
const isPromise = <T>(value: T | Promise<T>): value is Promise<T> =>
  typeof (value as { then?: unknown } | null)?.then === "function";

/**
 * Navigation state for one running app instance, held outside React.
 *
 * Records are keyed by session tab and dock instance. A record carries the
 * resource identity, its access context, the navigation parameters and a
 * monotonic revision, so a panel reads a committed result instead of publishing
 * requests while it renders. Only an explicit command advances a revision, an
 * unchanged record returns the same snapshot reference, and a record whose dock
 * closed, scope changed or runtime went away is cancelled rather than replayed.
 */
export class FileNavigationOwner {
  private records = new Map<string, Record>();
  /** One-shot operations per dock instance, keyed like records. */
  private operations = new Map<string, AbortController>();
  /** Survives record deletion so a reused dock tab id never repeats a generation. */
  private generations = new Map<string, number>();
  private listeners = new Set<() => void>();
  private disposed = false;
  private ports: FileNavigationPorts;

  constructor(ports: FileNavigationPorts) {
    this.ports = ports;
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  /** Stable per key: an unchanged record returns the same snapshot reference. */
  getSnapshot = (key: string): FileNavigationSnapshot | null => this.records.get(key)?.snapshot ?? null;

  /** Bind the workspace scope a dock shows; a different one starts a new lifetime. */
  bindScope(scope: FileNavigationScope, scopeKey: string): void {
    const record = this.ensure(scope);
    if (record.snapshot.scopeKey === scopeKey) return;
    if (record.snapshot.scopeKey === null) {
      const patched = { ...record.snapshot, scopeKey };
      record.snapshot = patched;
      this.notify();
      return;
    }
    this.reset(scope, scopeKey);
  }

  /**
   * Keep only the given dock keys, cancelling the rest. Called with the open
   * dock tabs of the active session, so a closed tab, another session or
   * another workspace ends its records instead of restoring stale access.
   */
  retain(openKeys: Iterable<string>): void {
    const keep = new Set(openKeys);
    let dropped = false;
    for (const [key, record] of Array.from(this.records)) {
      if (keep.has(key)) continue;
      this.drop(key, record);
      dropped = true;
    }
    for (const [key, operation] of Array.from(this.operations)) {
      if (keep.has(key)) continue;
      operation.abort();
      this.operations.delete(key);
    }
    if (dropped) this.notify();
  }

  /**
   * Begin a one-shot operation in a dock instance. A newer operation for the
   * same dock supersedes it, and the dock's lifetime ending cancels it; an
   * unrelated dock keeps running, so no panel cancels another's work.
   */
  beginOperation(scope: FileNavigationScope): FileNavigationOperation {
    const key = fileNavigationKey(scope);
    const previous = this.operations.get(key);
    previous?.abort();
    const controller = new AbortController();
    if (this.disposed) controller.abort();
    this.operations.set(key, controller);
    return {
      signal: controller.signal,
      owns: () => this.operations.get(key) === controller && !controller.signal.aborted,
      finish: () => { if (this.operations.get(key) === controller) this.operations.delete(key); },
    };
  }

  open(command: FileNavigationCommand): FileNavigationOutcome | Promise<FileNavigationOutcome> {
    if (this.disposed) return { status: "cancelled", reason: "disposed" };
    let scope: FileNavigationScope;
    try {
      scope = { sessionTabId: command.ref.tabId, dockTabId: this.ports.revealDock(command.ref) };
    } catch (error) {
      return { status: "failed", error: asError(error) };
    }
    const key = fileNavigationKey(scope);
    const record = this.ensure(scope);
    const operation = this.begin(record);
    let resolved: ResolvedFileResource | Promise<ResolvedFileResource>;
    try {
      resolved = this.ports.resolve(command.ref);
    } catch (error) {
      return this.settle(key, record, operation, SUPERSEDED, (): FileNavigationOutcome =>
        ({ status: "failed", error: asError(error) }));
    }
    if (!isPromise(resolved)) {
      return this.settle(key, record, operation, SUPERSEDED, (): FileNavigationOutcome => {
        this.commitNavigation(record, resolved, command.params);
        return { status: "opened", resource: resolved };
      });
    }
    return resolved.then(
      (resource) => this.settle(key, record, operation, SUPERSEDED, (): FileNavigationOutcome => {
        this.commitNavigation(record, resource, command.params);
        return { status: "opened", resource };
      }),
      // A resolution that failed after its dock went away is a cancellation:
      // the row that asked is gone, and its failure has nowhere to be shown.
      (error) => this.settle(key, record, operation, SUPERSEDED, (): FileNavigationOutcome =>
        ({ status: "failed", error: asError(error) })),
    );
  }

  /** Activate an open entry; it keeps the access context of the command that opened it. */
  selectEntry(scope: FileNavigationScope, path: string): void {
    const record = this.records.get(fileNavigationKey(scope));
    const entry = record?.snapshot.entries.find((candidate) => candidate.resource.path === path);
    if (!record || !entry) return;
    this.commitNavigation(record,entry.resource, {
      action: entry.source ? "source" : "preview",
      view: "files",
    });
  }

  /**
   * Select a path that carries no presentation of its own — a recent file or a
   * restored session. The read goes through the current workspace access, so a
   * path alone never re-grants the permissions of an earlier presentation.
   */
  selectPath(scope: FileNavigationScope, resource: FileResourceIdentityInput): void {
    const record = this.records.get(fileNavigationKey(scope));
    if (!record) return;
    this.commitNavigation(record,workspaceResource(resource, scope.sessionTabId), {
      action: "preview",
      view: "files",
    });
  }

  /** Switch an open tab between its preview and its source, reusing the tab. */
  setSourceMode(scope: FileNavigationScope, path: string, source: boolean): void {
    const record = this.records.get(fileNavigationKey(scope));
    const entry = record?.snapshot.entries.find((candidate) => candidate.resource.path === path);
    if (!record || !entry) return;
    this.commitNavigation(record, entry.resource, { action: source ? "source" : "preview", view: "files" });
  }

  /** Close one preview tab, selecting the neighbour the previous tab list implies. */
  closeEntry(scope: FileNavigationScope, path: string): void {
    const record = this.records.get(fileNavigationKey(scope));
    if (!record) return;
    const current = record.snapshot;
    const entries = current.entries.filter((entry) => entry.resource.path !== path);
    if (entries.length === current.entries.length) return;
    const selected = current.selected?.resource.path === path ? entries[entries.length - 1] ?? null : current.selected;
    this.commitState(record, { entries, selected });
  }

  /**
   * Drop every preview tab of this dock, as a scoped file or change list does.
   * The selection survives, so leaving the scope shows the file again without
   * a new command; `clearSelection` is what closes the preview itself.
   */
  clearEntries(scope: FileNavigationScope): void {
    const record = this.records.get(fileNavigationKey(scope));
    if (!record || record.snapshot.entries.length === 0) return;
    this.commitState(record, { entries: [] });
  }

  clearSelection(scope: FileNavigationScope): void {
    const record = this.records.get(fileNavigationKey(scope));
    if (!record || record.snapshot.selected === null) return;
    this.commitState(record, { selected: null });
  }

  /**
   * Restore remembered paths for a dock no command has opened anything in yet.
   * Restored entries carry workspace access only; a record a command already
   * wrote to is left alone, so a restore never outranks a live navigation.
   */
  restore(scope: FileNavigationScope, state: FileNavigationRestore): void {
    const record = this.records.get(fileNavigationKey(scope));
    if (!record) return;
    const current = record.snapshot;
    if (current.entries.length > 0 || current.selected) return;
    const entries = state.paths.map((path): FilePreviewEntry => ({
      resource: workspaceResource({ hostId: state.hostId, path }, scope.sessionTabId),
      source: false,
    }));
    if (!entries.length) return;
    this.commitState(record, {
      entries,
      selected: state.selectedPath
        ? entries.find((entry) => entry.resource.path === state.selectedPath) ?? null
        : null,
    });
  }

  dispose(): void {
    this.disposed = true;
    for (const [key, record] of Array.from(this.records)) this.drop(key, record);
    this.records.clear();
    for (const operation of Array.from(this.operations.values())) operation.abort();
    this.operations.clear();
    this.notify();
  }

  private ensure(scope: FileNavigationScope): Record {
    const key = fileNavigationKey(scope);
    const existing = this.records.get(key);
    if (existing) return existing;
    const lifetime = new AbortController();
    const record: Record = {
      lifetime,
      pending: null,
      navigationRevision: 0,
      snapshot: {
        scopeKey: null,
        sessionTabId: scope.sessionTabId,
        dockTabId: scope.dockTabId,
        generation: this.generations.get(scope.dockTabId) ?? 0,
        entries: [],
        selected: null,
        sourcePaths: [],
        navigation: null,
        revision: 0,
        contentRevision: 0,
        treeReveal: 0,
        signal: lifetime.signal,
      },
    };
    this.records.set(key, record);
    return record;
  }

  private reset(scope: FileNavigationScope, scopeKey: string): void {
    const key = fileNavigationKey(scope);
    const previous = this.records.get(key);
    if (previous) this.drop(key, previous);
    const record = this.ensure(scope);
    record.snapshot = { ...record.snapshot, scopeKey };
    this.notify();
  }

  private begin(record: Record): AbortController {
    record.pending?.abort();
    const operation = new AbortController();
    record.pending = operation;
    return operation;
  }

  /** Commit only while this operation still owns the record; otherwise it lost. */
  private settle<T>(
    key: string,
    record: Record,
    operation: AbortController,
    lost: FileNavigationOutcome,
    commit?: () => T,
  ): T | FileNavigationOutcome {
    if (this.records.get(key) !== record || record.pending !== operation || operation.signal.aborted) return lost;
    record.pending = null;
    return commit ? commit() : lost;
  }

  private commitNavigation(
    record: Record,
    resource: ResolvedFileResource,
    params: FileNavigationParams,
  ): void {
    this.apply(record, (current) => {
      const { entries, selected } = upsertEntry(current.entries, resource, params.action === "source");
      const target = params.view === "files" ? selected : current.selected;
      // Every explicit command is delivered as its own navigation revision: a
      // repeat must still reveal the file when the dock moved on. Only the read
      // inputs decide whether the preview has to load its content again.
      record.navigationRevision += 1;
      return {
        ...current,
        entries,
        selected: target,
        sourcePaths: sourcePathsOf(entries),
        navigation: { revision: record.navigationRevision, resource, params },
        revision: current.revision + 1,
        contentRevision: current.contentRevision + (target !== current.selected ? 1 : 0),
        treeReveal: params.action === "reveal-tree" ? current.treeReveal + 1 : current.treeReveal,
      };
    });
  }

  private commitState(
    record: Record,
    patch: { entries?: readonly FilePreviewEntry[]; selected?: FilePreviewEntry | null },
  ): void {
    this.apply(record, (current) => {
      const entries = patch.entries ?? current.entries;
      const selected = patch.selected !== undefined ? patch.selected : current.selected;
      if (entries === current.entries && selected === current.selected) return null;
      return {
        ...current,
        entries,
        selected,
        sourcePaths: sourcePathsOf(entries),
        revision: current.revision + 1,
        contentRevision: current.contentRevision + (selected !== current.selected ? 1 : 0),
      };
    });
  }

  private apply(record: Record, reduce: (current: FileNavigationSnapshot) => FileNavigationSnapshot | null): void {
    const next = reduce(record.snapshot);
    if (!next) return;
    record.snapshot = next;
    this.notify();
  }

  private drop(key: string, record: Record): void {
    record.pending?.abort();
    record.lifetime.abort();
    this.records.delete(key);
    this.generations.set(record.snapshot.dockTabId, record.snapshot.generation + 1);
  }

  private notify(): void {
    for (const listener of Array.from(this.listeners)) listener();
  }
}

export type FileResourceIdentityInput = Readonly<{ hostId: string; path: string }>;

function workspaceResource(resource: FileResourceIdentityInput, sessionTabId: string): ResolvedFileResource {
  const access: FileAccessContext = { source: "workspace", tabId: sessionTabId };
  return { hostId: resource.hostId, path: resource.path, requestedPath: resource.path, access };
}

function sameResource(left: ResolvedFileResource, right: ResolvedFileResource): boolean {
  return left.hostId === right.hostId
    && left.path === right.path
    && sameAccessContext(left.access, right.access);
}

const sourcePathsOf = (entries: readonly FilePreviewEntry[]): readonly string[] =>
  entries.filter((entry) => entry.source).map((entry) => entry.resource.path);

/**
 * Move an entry to the most-recent position. An entry whose resource and mode
 * are unchanged keeps its identity, and an unchanged list keeps its array
 * reference, so a repeated command leaves every derived value — including the
 * preview's read inputs — exactly as it was.
 */
function upsertEntry(
  entries: readonly FilePreviewEntry[],
  resource: ResolvedFileResource,
  source: boolean,
): { entries: readonly FilePreviewEntry[]; selected: FilePreviewEntry } {
  const existing = entries.find((candidate) => candidate.resource.path === resource.path);
  const entry = existing && existing.source === source && sameResource(existing.resource, resource)
    ? existing
    : { resource, source };
  const next = [...entries.filter((candidate) => candidate.resource.path !== resource.path), entry]
    .slice(-FILE_PREVIEW_LIMIT);
  const unchanged = next.length === entries.length && next.every((candidate, index) => candidate === entries[index]);
  return { entries: unchanged ? entries : next, selected: entry };
}
