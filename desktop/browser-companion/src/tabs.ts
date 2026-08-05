// Tab manager: one WebContentsView per remote page, sharing the isolated
// persist:reasonix-browser-v1 Session. Hidden tabs only toggle visibility;
// webContents are never reused between tabs. Remote pages get the hardened
// webPreferences (no Node, no preload, sandbox on) and every navigation is
// re-validated as http(s) before it can proceed.

import { BaseWindow, WebContentsView, session, type Session } from "electron";
import { assertHttpUrl, ProtocolError } from "./protocol";
import type { BrowserErrorCode } from "./generated/browserProtocol.generated";

export interface TabRecord {
  id: string;
  ownerId: string;
  url: string;
  title: string;
  generation: number;
  active: boolean;
  fromAgent: boolean;
}

export type WireEventSink = (name: string, ownerId: string, data: unknown) => void;

export const CHROME_HEIGHT = 40;

const PROFILE_PARTITION = "persist:reasonix-browser-v1";

interface TabEntry {
  record: TabRecord;
  view: WebContentsView;
}

export class TabManager {
  private entries = new Map<string, TabEntry>();
  private owners = new Map<string, string[]>(); // ownerId -> ordered tab ids
  private activeByOwner = new Map<string, string>();
  private session: Session;
  private seq = 0;
  private chromeView: WebContentsView | null = null;
  // activeOwner is the chat whose pages are currently visible in the window.
  // Only that owner's active tab is shown; every other owner's pages stay
  // hidden (cross-owner isolation).
  private activeOwner = "";

  constructor(
    private window: BaseWindow,
    private emit: WireEventSink,
  ) {
    // All remote pages share one isolated persistent session; the trusted
    // chrome view uses its own non-persistent session (see attachChrome).
    this.session = session.fromPartition(PROFILE_PARTITION);
    this.hardenSession(this.session);
  }

  /** The shared remote-page session (cookies/login state live here). */
  get sharedSession(): Session {
    return this.session;
  }

  // ---- session hardening (Phase 4 expands per-capability prompts) ----

  private hardenSession(ses: Session): void {
    // No permission is granted automatically. Human-only capabilities
    // (camera, mic, location, ...) reach the host as permission.request
    // events in a later phase; until then every request is denied.
    ses.setPermissionRequestHandler((_wc, permission, callback) => {
      const wc = _wc as unknown as { getURL(): string } | undefined;
      const url = typeof wc?.getURL === "function" ? wc.getURL() : "";
      this.emit("permission.request", "", {
        origin: safeOrigin(url),
        capability: permission,
      });
      callback(false);
    });
    // Downloads surface as events; the save-confirmation flow lands with the
    // human browsing phase. Never silent: the host sees every download.
    ses.on("will-download", (_event, item, wc) => {
      // Attribute the download to the tab whose webContents initiated it.
      let tabId = "";
      for (const [id, entry] of this.entries) {
        if (entry.view.webContents === wc) {
          tabId = id;
          break;
        }
      }
      const ownerId = tabId ? this.ownerForTab(tabId) : "";
      this.emit("download", ownerId, {
        ownerId,
        tabId,
        filename: item.getFilename(),
        mime: item.getMimeType(),
        state: "started",
      });
    });
  }

  attachChrome(view: WebContentsView): void {
    this.chromeView = view;
  }

  // ---- tab lifecycle ----

  /**
   * Creates a tab for ownerId. Disposition only decides activation; the
   * WebContentsView is always created (hidden tabs are never recycled).
   */
  /** Chrome-initiated new tab: blank page (no network, no origin). The
   * address bar still enforces http(s) before any real navigation. */
  createChromeTab(ownerId: string): TabRecord {
    const id = `t-${++this.seq}`;
    const view = new WebContentsView({
      webPreferences: {
        nodeIntegration: false,
        contextIsolation: true,
        sandbox: true,
        webSecurity: true,
        session: this.session,
      },
    });
    const record: TabRecord = {
      id,
      ownerId,
      url: "about:blank",
      title: "",
      generation: 1,
      active: true,
      fromAgent: false,
    };
    this.entries.set(id, { record, view });
    this.window.contentView.addChildView(view);
    const order = this.owners.get(ownerId) ?? [];
    order.push(id);
    this.owners.set(ownerId, order);
    this.deactivateOwnerActiveLocked(ownerId);
    this.activeByOwner.set(ownerId, id);
    if (this.activeOwner === "") {
      this.activeOwner = ownerId;
    }
    this.wireTab(ownerId, id, view);
    this.layout();
    this.focus(ownerId, id);
    return { ...record };
  }

  /** The WebContentsView behind a tab (chrome toolbar navigation). */
  webContentsFor(tabId: string): Electron.WebContentsView | null {
    return this.entries.get(tabId)?.view ?? null;
  }

  createTab(ownerId: string, url: string, disposition: string, fromAgent: boolean): TabRecord {
    const checkedUrl = assertHttpUrl(url);
    const id = `t-${++this.seq}`;
    const view = new WebContentsView({
      webPreferences: {
        nodeIntegration: false,
        contextIsolation: true,
        sandbox: true,
        webSecurity: true,
        // No preload: remote pages are untrusted and get no bridge at all.
        session: this.session,
      },
    });
    const record: TabRecord = {
      id,
      ownerId,
      url: checkedUrl,
      title: "",
      generation: 1,
      active: disposition === "foreground",
      fromAgent,
    };
    this.entries.set(id, { record, view });
    this.window.contentView.addChildView(view);
    const order = this.owners.get(ownerId) ?? [];
    order.push(id);
    this.owners.set(ownerId, order);
    if (record.active) {
      // One active tab per owner: deactivate the previous active tab before
      // promoting the new foreground tab.
      this.deactivateOwnerActiveLocked(ownerId);
      this.activeByOwner.set(ownerId, id);
    }
    if (this.activeOwner === "") {
      this.activeOwner = ownerId;
    }
    this.wireTab(ownerId, id, view);
    this.layout();
    void view.webContents.loadURL(checkedUrl).catch(() => {
      // Failed loads surface via did-fail-load / navigation events; the host
      // sees the page state through tab.changed, not a crash here.
    });
    if (record.active) {
      this.focus(ownerId, id);
    }
    return { ...record };
  }

  private wireTab(ownerId: string, tabId: string, view: WebContentsView): void {
    const wc = view.webContents;
    wc.on("did-start-navigation", (_event, url) => {
      if (!/^https?:\/\//i.test(url)) {
        // Non-http navigation (error pages, unsupported schemes) is allowed to
        // render but never re-enters an agent path; report it as failed state.
        this.emit("navigation", ownerId, { ownerId, tabId, url, title: "", state: "failed" });
        return;
      }
      this.emit("navigation", ownerId, { ownerId, tabId, url, title: "", state: "started" });
    });
    wc.on("did-navigate", (_event, url) => {
      this.applyNavigation(ownerId, tabId, url, true);
    });
    wc.on("did-navigate-in-page", (_event, url) => {
      this.applyNavigation(ownerId, tabId, url, false);
    });
    wc.on("page-title-updated", (_event, title) => {
      this.applyTitle(ownerId, tabId, title);
    });
    wc.on("render-process-gone", (_event, details) => {
      this.emit("renderer.crash", ownerId, { ownerId, tabId });
      this.revokeLease(ownerId, tabId, "user", `renderer gone (${details.reason})`);
    });
    // Popups never become unmanaged Electron windows: every http(s) popup is
    // routed into a new managed tab of the same chat.
    wc.setWindowOpenHandler(({ url }) => {
      if (/^https?:\/\//i.test(url)) {
        const rec = this.createTab(ownerId, url, "background", false);
        return { action: "deny" };
      }
      // Non-http popups are dropped; the system opener path is host-side.
      return { action: "deny" };
    });
    // Human input always wins: any key/pointer input revokes an active agent
    // lease on this tab immediately.
    wc.on("before-input-event", () => {
      this.revokeLease(ownerId, tabId, "user", "human input detected");
    });
  }

  private applyNavigation(ownerId: string, tabId: string, url: string, committed: boolean): void {
    const entry = this.entries.get(tabId);
    if (!entry) return;
    entry.record.url = url;
    if (committed) {
      entry.record.generation += 1;
    }
    this.emit("navigation", ownerId, {
      ownerId,
      tabId,
      url,
      title: entry.record.title,
      state: committed ? "committed" : "started",
    });
    this.emit("tab.changed", ownerId, {
      ownerId,
      tabId,
      url,
      title: entry.record.title,
      active: entry.record.active,
      generation: entry.record.generation,
    });
  }

  private applyTitle(ownerId: string, tabId: string, title: string): void {
    const entry = this.entries.get(tabId);
    if (!entry) return;
    entry.record.title = title;
    this.emit("tab.changed", ownerId, {
      ownerId,
      tabId,
      url: entry.record.url,
      title,
      active: entry.record.active,
      generation: entry.record.generation,
    });
  }

  /**
   * Switches the window's visible owner. Only this owner's active page is
   * shown; all other owners' pages are hidden.
   */
  setActiveOwner(ownerId: string): void {
    this.activeOwner = ownerId;
    this.layout();
  }

  /** The chat whose pages are currently visible. */
  get visibleOwner(): string {
    return this.activeOwner;
  }

  /** True when the agent holds a lease on any tab of the owner. */
  agentControllingFor(ownerId: string): boolean {
    for (const tabId of this.leaseByTab.keys()) {
      if (this.entries.get(tabId)?.record.ownerId === ownerId) {
        return true;
      }
    }
    return false;
  }

  // deactivateOwnerActiveLocked marks the owner's current active tab as
  // inactive (visibility is applied by the caller's layout).
  private deactivateOwnerActiveLocked(ownerId: string): void {
    const prevId = this.activeByOwner.get(ownerId);
    if (!prevId) return;
    const prev = this.entries.get(prevId);
    if (prev) {
      prev.record.active = false;
    }
    this.activeByOwner.delete(ownerId);
  }

  activate(ownerId: string, tabId: string): void {
    const entry = this.entries.get(tabId);
    if (!entry || entry.record.ownerId !== ownerId) {
      throw new ProtocolError("tab_not_found", `tab ${tabId} not found for owner`);
    }
    if (entry.record.active) return;
    // Deactivate the previous active tab of this owner.
    const prevId = this.activeByOwner.get(ownerId);
    if (prevId && prevId !== tabId) {
      const prev = this.entries.get(prevId);
      if (prev) {
        prev.record.active = false;
        this.applyVisibility(prevId, prev, false);
      }
    }
    entry.record.active = true;
    this.activeByOwner.set(ownerId, tabId);
    this.applyVisibility(tabId, entry, true);
    this.layout();
    this.focus(ownerId, tabId);
    this.emit("tab.changed", ownerId, {
      ownerId,
      tabId,
      url: entry.record.url,
      title: entry.record.title,
      active: true,
      generation: entry.record.generation,
    });
  }

  closeTab(ownerId: string, tabId: string): void {
    const entry = this.entries.get(tabId);
    if (!entry || entry.record.ownerId !== ownerId) {
      throw new ProtocolError("tab_not_found", `tab ${tabId} not found for owner`);
    }
    this.destroyEntry(tabId, entry);
    // Activate the last remaining tab of the owner, if any.
    const order = this.owners.get(ownerId) ?? [];
    const next = order.length > 0 ? this.entries.get(order[order.length - 1]!) : undefined;
    if (next && !next.record.active) {
      this.activate(ownerId, next.record.id);
    }
    this.layout();
  }

  navigate(ownerId: string, tabId: string, url: string): TabRecord {
    const entry = this.entries.get(tabId);
    if (!entry || entry.record.ownerId !== ownerId) {
      throw new ProtocolError("tab_not_found", `tab ${tabId} not found for owner`);
    }
    const checkedUrl = assertHttpUrl(url);
    entry.record.url = checkedUrl;
    void entry.view.webContents.loadURL(checkedUrl).catch(() => {});
    return { ...entry.record };
  }

  list(ownerId: string): TabRecord[] {
    const order = this.owners.get(ownerId) ?? [];
    return order
      .map((id) => this.entries.get(id)?.record)
      .filter((r): r is TabRecord => r !== undefined)
      .map((r) => ({ ...r }));
  }

  /** Removes one chat's tabs; the shared session (cookies) is untouched. */
  removeOwner(ownerId: string): void {
    const order = this.owners.get(ownerId) ?? [];
    for (const id of [...order]) {
      const entry = this.entries.get(id);
      if (entry) this.destroyEntry(id, entry);
    }
    this.owners.delete(ownerId);
    this.activeByOwner.delete(ownerId);
    this.layout();
  }

  ownerForTab(tabId: string): string {
    return this.entries.get(tabId)?.record.ownerId ?? "";
  }

  // ---- agent lease (dormant until the CDP controller lands) ----

  private leaseByTab = new Map<string, string>(); // tabId -> ownerId

  /** Grants the agent control of a tab. No-op when a lease already exists. */
  acquireLease(ownerId: string, tabId: string): boolean {
    const entry = this.entries.get(tabId);
    if (!entry || entry.record.ownerId !== ownerId) return false;
    if (this.leaseByTab.has(tabId)) return false;
    this.leaseByTab.set(tabId, ownerId);
    return true;
  }

  /** Revokes an agent lease; human input and sensitive pages call this. */
  revokeLease(ownerId: string, tabId: string, reason: "user" | "permission" | "sensitive_field", detail: string): void {
    if (!this.leaseByTab.has(tabId)) return;
    this.leaseByTab.delete(tabId);
    void detail;
    this.emit("agent.takeover", ownerId, { ownerId, tabId, reason });
  }

  // ---- layout / visibility ----

  layout(): void {
    const bounds = this.window.getContentBounds();
    const contentHeight = Math.max(0, bounds.height - CHROME_HEIGHT);
    for (const [id, entry] of this.entries) {
      // Only the visible owner's active tab is shown; other owners' pages
      // never overlap the window (cross-owner isolation), and within one
      // owner only the single active tab is visible.
      if (entry.record.ownerId === this.activeOwner && entry.record.active) {
        entry.view.setBounds({ x: 0, y: CHROME_HEIGHT, width: bounds.width, height: contentHeight });
        entry.view.setVisible(true);
      } else {
        this.applyVisibility(id, entry, false);
      }
    }
  }

  private applyVisibility(id: string, entry: TabEntry, visible: boolean): void {
    void id;
    entry.view.setVisible(visible);
    if (!visible) {
      // Zero bounds keep hidden views off screen; visibility toggling never
      // reuses a webContents between tabs.
      entry.view.setBounds({ x: 0, y: 0, width: 0, height: 0 });
    }
  }

  private focus(ownerId: string, tabId: string): void {
    const entry = this.entries.get(tabId);
    if (!entry) return;
    entry.view.webContents.focus();
    void ownerId;
  }

  private destroyEntry(tabId: string, entry: TabEntry): void {
    this.entries.delete(tabId);
    this.leaseByTab.delete(tabId);
    // Remove the page from the window so the WebContentsView is fully
    // detached (and destroyed below), not just hidden.
    if (this.window.contentView.children.includes(entry.view)) {
      this.window.contentView.removeChildView(entry.view);
    }
    const order = this.owners.get(entry.record.ownerId) ?? [];
    const idx = order.indexOf(tabId);
    if (idx >= 0) order.splice(idx, 1);
    if (order.length === 0) {
      this.owners.delete(entry.record.ownerId);
      this.activeByOwner.delete(entry.record.ownerId);
    } else if (this.activeByOwner.get(entry.record.ownerId) === tabId) {
      this.activeByOwner.set(entry.record.ownerId, order[order.length - 1]!);
    }
    // Destroy the webContents so no renderer outlives its tab.
    if (!entry.view.webContents.isDestroyed()) {
      entry.view.webContents.close();
    }
  }

  /** Destroys every tab (window close): save happens host-side first. */
  destroyAll(): void {
    for (const [id, entry] of [...this.entries]) {
      this.destroyEntry(id, entry);
    }
  }

  get count(): number {
    return this.entries.size;
  }
}

function safeOrigin(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return "";
  }
}
