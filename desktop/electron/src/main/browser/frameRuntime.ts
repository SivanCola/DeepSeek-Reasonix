import { randomUUID } from "node:crypto";
import { AsyncLocalStorage } from "node:async_hooks";
import type { GuestFrame, GuestPage } from "./guestView.js";
import { acquireDebugger, type DebuggerRelease, type DebuggerSender } from "./debuggerLease.js";
import { browserFailure } from "./errors.js";
import { abortable } from "./captureQueue.js";

const operationSignals = new AsyncLocalStorage<AbortSignal>();
export function withFrameOperationSignal<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
  return operationSignals.run(signal, work);
}

interface Context { frameId: string; sessionId?: string; executionContextId: number }
type Execute = (contextId: number, sessionId: string | undefined, send: DebuggerSender) => Promise<unknown>;

// One operation owns its frame contexts and child sessions; the page's root
// connection is owned by debuggerLease. No context survives an operation.
export class FrameRuntime {
  private readonly release: DebuggerRelease;
  private readonly main: GuestFrame;
  private readonly sessions = new Map<string, string>();
  private readonly contexts = new Map<string, Promise<Context>>();
  private readonly objectSessions = new Set<string | undefined>();
  private readonly group = `reasonix-frame-owner-${randomUUID()}`;
  private root?: Promise<Context>;
  private closed = false;
  private readonly signal = operationSignals.getStore();

  constructor(private readonly page: GuestPage, private readonly deadline = Date.now() + 1000) {
    this.main = page.mainFrame;
    this.release = acquireDebugger(page.debugger);
  }

  private assertLive(): void {
    this.signal?.throwIfAborted();
    if (this.closed || this.page.isDestroyed() || this.main.detached || this.page.mainFrame !== this.main) throw browserFailure("stale_document", "frame operation no longer owns the page");
    if (Date.now() >= this.deadline) throw browserFailure("page_not_ready", "child frame protocol deadline exceeded");
  }

  private async send(method: string, params?: unknown, sessionId?: string): Promise<unknown> {
    this.assertLive();
    const command = this.release.send(method, params, sessionId);
    // A timed-out attach can still allocate a session. Consume and release its
    // late result without allowing it back into the completed operation.
    if (method === "Target.attachToTarget") void command.then(result => {
      const attached = result as { sessionId?: string };
      if ((this.closed || this.signal?.aborted || Date.now() >= this.deadline) && attached.sessionId) void this.release.send("Target.detachFromTarget", { sessionId: attached.sessionId }).catch(() => {});
    }).catch(() => {});
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const result = await Promise.race([this.signal ? abortable(command, this.signal) : command, new Promise<never>((_resolve, reject) => {
        timer = setTimeout(() => reject(browserFailure("page_not_ready", `child frame protocol deadline exceeded at ${method}`)), Math.max(1, this.deadline - Date.now()));
      })]);
      this.assertLive();
      return result;
    } finally { clearTimeout(timer); }
  }

  private context(frameId: string, sessionId?: string): Promise<Context> {
    const key = `${sessionId ?? "root"}:${frameId}`;
    let existing = this.contexts.get(key);
    if (!existing) {
      existing = this.send("Page.createIsolatedWorld", { frameId, worldName: "reasonix-browser-v1", grantUniveralAccess: false }, sessionId).then(world => {
        const { executionContextId } = world as { executionContextId: number };
        if (!Number.isInteger(executionContextId)) throw browserFailure("script_runtime_error", "isolated world returned no context");
        return { frameId, sessionId, executionContextId };
      });
      this.contexts.set(key, existing);
    }
    return existing;
  }

  private rootContext(): Promise<Context> {
    return this.root ??= (async () => {
      await this.send("Page.enable");
      const tree = await this.send("Page.getFrameTree") as { frameTree: { frame: { id: string } } };
      return this.context(tree.frameTree.frame.id);
    })();
  }

  private async child(parent: Context, nativeParent: GuestFrame, nativeChild: GuestFrame): Promise<Context> {
    const url = nativeChild.url;
    const siblings = () => nativeParent.frames ?? nativeParent.framesInSubtree.filter(frame => frame.parent?.frameTreeNodeId === nativeParent.frameTreeNodeId);
    const index = siblings().findIndex(frame => frame.frameTreeNodeId === nativeChild.frameTreeNodeId);
    const verify = () => {
      this.assertLive();
      if (index < 0 || siblings()[index]?.frameTreeNodeId !== nativeChild.frameTreeNodeId || nativeChild.detached || nativeParent.detached || nativeChild.url !== url || nativeChild.parent?.frameTreeNodeId !== nativeParent.frameTreeNodeId) throw browserFailure("stale_document", "frame changed during resolution");
    };
    verify();
    const owner = await this.send("Runtime.evaluate", { contextId: parent.executionContextId, expression: `(() => { const target = window.frames[${index}]; const find = root => { for (const el of root.querySelectorAll('*')) { if ((el.tagName === 'IFRAME' || el.tagName === 'FRAME') && el.contentWindow === target) return el; if (el.shadowRoot) { const nested = find(el.shadowRoot); if (nested) return nested; } } }; return find(document); })()`, returnByValue: false, objectGroup: this.group, timeout: 500 }, parent.sessionId) as { result?: { objectId?: string } };
    if (!owner.result?.objectId) throw browserFailure("stale_document", "frame owner disappeared");
    this.objectSessions.add(parent.sessionId);
    const describe = async () => {
      verify();
      const result = await this.send("DOM.describeNode", { objectId: owner.result!.objectId }, parent.sessionId) as { node: { frameId?: string; backendNodeId?: number } };
      if (!result.node.frameId) throw browserFailure("stale_document", "frame owner has no document");
      return result.node as { frameId: string; backendNodeId?: number };
    };
    const original = await describe();
    let frameId = original.frameId;
    const targets = await this.send("Target.getTargets") as { targetInfos: { targetId: string; type: string }[] };
    if (!targets.targetInfos.some(target => target.targetId === frameId && target.type === "iframe")) return this.context(frameId, parent.sessionId);
    let sessionId = this.sessions.get(frameId);
    if (!sessionId) {
      let attached: { sessionId: string };
      try { attached = await this.send("Target.attachToTarget", { targetId: frameId, flatten: true }) as { sessionId: string }; }
      catch (error) {
        // Refresh only a failed read-only target attachment. The same retained
        // DOM object and native frame must still identify the original owner.
        verify();
        await this.send("Target.getTargets");
        const refreshed = await describe();
        if (original.backendNodeId === undefined || original.backendNodeId !== refreshed.backendNodeId || refreshed.frameId === frameId) throw error;
        frameId = refreshed.frameId;
        attached = await this.send("Target.attachToTarget", { targetId: frameId, flatten: true }) as { sessionId: string };
      }
      sessionId = attached.sessionId;
      this.sessions.set(frameId, sessionId);
      await this.send("Page.enable", {}, sessionId);
      await this.send("Runtime.enable", {}, sessionId);
      await this.send("DOM.enable", {}, sessionId);
    }
    verify();
    return this.context(frameId, sessionId);
  }

  async run(frame: GuestFrame, code: string, execute?: Execute): Promise<unknown> {
    this.assertLive();
    const url = frame.url;
    const path: GuestFrame[] = [];
    for (let current = frame; current.frameTreeNodeId !== this.main.frameTreeNodeId;) {
      path.unshift(current);
      if (!current.parent) throw browserFailure("stale_document", "frame no longer has a parent");
      current = current.parent;
    }
    let context = await this.rootContext();
    let parent = this.main;
    for (const child of path) { context = await this.child(context, parent, child); parent = child; }
    const assertFrame = () => {
      this.assertLive();
      if (frame.detached || frame.url !== url) throw browserFailure("stale_document", "frame navigated during evaluation");
    };
    assertFrame();
    if (execute) {
      // Custom operations (file uploads) must use the same deadline and
      // cancellation fence as script evaluation. Never expose the raw lease
      // sender: a late lookup could otherwise dispatch into a departed frame.
      const send: DebuggerSender = async (method, params, sessionId) => {
        assertFrame();
        const result = await this.send(method, params, sessionId);
        assertFrame();
        return result;
      };
      const result = await execute(context.executionContextId, context.sessionId, send);
      assertFrame();
      return result;
    }
    const result = await this.send("Runtime.evaluate", { expression: code, contextId: context.executionContextId, returnByValue: true, awaitPromise: true, timeout: Math.max(1, this.deadline - Date.now()) }, context.sessionId) as { result?: { value?: unknown }; exceptionDetails?: unknown };
    if (result.exceptionDetails) throw browserFailure("script_runtime_error", "isolated child-frame runtime failed");
    assertFrame();
    return result.result?.value;
  }

  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    // Cleanup commands are tracked by the connection owner even after this
    // operation's deadline. Their eventual replies cannot resurrect contexts.
    const cleanup = (async () => {
      const targets: Array<string | undefined> = [...this.sessions.values()].reverse();
      targets.push(undefined);
      for (const sessionId of targets) {
        // Never detach while releaseObjectGroup is still using that session.
        if (this.objectSessions.has(sessionId)) await this.release.send("Runtime.releaseObjectGroup", { objectGroup: this.group }, sessionId).catch(() => {});
        if (sessionId) await this.release.send("Target.detachFromTarget", { sessionId }).catch(() => {});
      }
    })();
    let timer: ReturnType<typeof setTimeout> | undefined;
    try { await Promise.race([cleanup, new Promise<void>(resolve => { timer = setTimeout(resolve, 100); })]); }
    finally { clearTimeout(timer); this.contexts.clear(); this.sessions.clear(); this.release(); }
  }
}

export async function runChildFrame(page: GuestPage, frame: GuestFrame, code: string, execute?: Execute): Promise<unknown> {
  const runtime = new FrameRuntime(page);
  try { return await runtime.run(frame, code, execute); }
  finally { await runtime.close(); }
}
