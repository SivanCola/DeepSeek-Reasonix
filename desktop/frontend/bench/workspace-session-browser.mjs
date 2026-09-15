import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH ??= path.join(root, ".pw-browsers");
const { chromium } = await import("playwright");
const server = await createServer({ root, logLevel: "error", server: { host: "127.0.0.1", port: 0 }, plugins: [{
  name: "sidebar-fixture",
  configureServer(server) {
    server.middlewares.use("/__sidebar_fixture", (_req, res) => {
      res.setHeader("Content-Type", "text/html");
      res.end('<html><head></head><body><div id="sidebar" style="width:280px;height:700px"></div><script type="module">import RefreshRuntime from "/@react-refresh"; RefreshRuntime.injectIntoGlobalHook(window); window.$RefreshReg$ = () => {}; window.$RefreshSig$ = () => (type) => type; window.__vite_plugin_react_preamble_installed__ = true;</script></body></html>');
    });
  },
}] });
await server.listen();
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 900, height: 800 } });
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(`${server.resolvedUrls.local[0]}__sidebar_fixture`);
  await page.evaluate(async () => {
    await import("/src/styles.css");
    const { default: React } = await import("/node_modules/.vite/deps/react.js");
    const { default: { createRoot } } = await import("/node_modules/.vite/deps/react-dom_client.js");
    const { installDesktopHostStub } = await import("/src/__tests__/desktopHostStub.ts");
    const { LocaleProvider } = await import("/src/lib/i18n.tsx");
    const { ToastProvider } = await import("/src/lib/toast.tsx");
    const rows = Array.from({ length: 7 }, (_, i) => ({
      ref: { hostId: "local", sessionId: `session-${i}` }, workspaceId: "global",
      title: i === 0 ? "修复会话历史加载和跨工作区切换时的启动错误" : "",
      preview: i === 0 ? "different preview must not duplicate the title" : `用户的实际请求 ${i}`,
      turns: 2, createdAt: Date.now() - 3600000, updatedAt: Date.now() - 120000,
      blank: false, archived: false, running: false, metadataStatus: "ready", health: "ready",
    }));
    window.sidebarCalls = [];
    let workspaces = [
      { id: "global", title: "shared-amazon-appointment-system", root: "", visible: true },
      { id: "project-a", title: "Project A", root: "/a", visible: true },
      { id: "project-b", title: "Project B", root: "/b", visible: true },
    ];
    window.workspaceMoves = [];
    installDesktopHostStub({
      GetLocale: async () => "zh",
      GetWorkspaceSnapshot: async () => ({ generation: 1, workspaces: [...workspaces], archivedSessionIds: [], pendingCreates: [] }),
      ListTabs: async () => [{ active: true, sessionId: "session-0" }],
      ListWorkspaceSessions: async (id, query, _cursor, _limit, archived) => ({ sessions: rows.filter(row => row.workspaceId === id && row.archived === archived && `${row.title} ${row.preview}`.includes(query)) }),
      MoveWorkspace: async (id, before) => {
        if (window.failWorkspaceMove) throw new Error("Workspace order could not be saved");
        window.workspaceMoves.push([id, before]);
        const moving = workspaces.find(item => item.id === id);
        workspaces = workspaces.filter(item => item.id !== id);
        const index = before ? workspaces.findIndex(item => item.id === before) : workspaces.length;
        workspaces.splice(index, 0, moving);
      },
      OpenSession: async ref => { window.sidebarCalls.push(ref.sessionId); },
      ArchiveCanonicalSession: async ref => { rows.find(row => row.ref.sessionId === ref.sessionId).archived = true; },
      RestoreCanonicalSession: async ref => { rows.find(row => row.ref.sessionId === ref.sessionId).archived = false; },
    });
    const { WorkspaceSessionBrowser } = await import("/src/components/WorkspaceSessionBrowser.tsx");
    const reactRoot = createRoot(document.getElementById("sidebar"));
    const navigation = { onOpenSession: async ref => { window.sidebarCalls.push(ref.sessionId); }, onCreateSession: async () => {} };
    window.renderArchived = () => reactRoot.render(React.createElement(LocaleProvider, null, React.createElement(ToastProvider, null, React.createElement(WorkspaceSessionBrowser, { ...navigation, archived: true }))));
    reactRoot.render(React.createElement(LocaleProvider, null, React.createElement(ToastProvider, null, React.createElement(WorkspaceSessionBrowser, navigation))));
  });
  const rows = page.locator(".workspace-browser__session");
  await rows.first().waitFor();
  assert.equal(await rows.count(), 5);
  for (const theme of ["light", "dark"]) {
    for (const width of [220, 300]) {
      await page.evaluate(({ width, theme }) => {
        document.getElementById("sidebar").style.width = `${width}px`;
        document.documentElement.dataset.theme = theme;
      }, { width, theme });
      const geometry = await rows.first().evaluate(row => {
        const label = row.querySelector(".workspace-browser__session-label");
        const time = row.querySelector(".workspace-browser__session-time");
        return { height: row.getBoundingClientRect().height, overflow: row.scrollWidth > row.clientWidth,
          whiteSpace: getComputedStyle(label).whiteSpace, ellipsis: getComputedStyle(label).textOverflow,
          labelRight: label.getBoundingClientRect().right, timeLeft: time.getBoundingClientRect().left };
      });
      assert.ok(geometry.height <= 32, JSON.stringify(geometry));
      assert.equal(geometry.overflow, false);
      assert.equal(geometry.whiteSpace, "nowrap");
      assert.equal(geometry.ellipsis, "ellipsis");
      assert.ok(geometry.labelRight <= geometry.timeLeft);
      const folder = await page.locator(".workspace-browser__workspace-heading").first().evaluate(row => {
        const button = row.querySelector(".workspace-browser__workspace-title");
        const label = row.querySelector(".workspace-browser__workspace-label");
        const arrow = button.querySelector("svg").getBoundingClientRect();
        const plus = row.querySelector(".workspace-browser__workspace-create").getBoundingClientRect();
        const rect = label.getBoundingClientRect();
        return { title: button.title, whitespace: getComputedStyle(label).whiteSpace,
          ellipsis: getComputedStyle(label).textOverflow, truncated: label.scrollWidth > label.clientWidth,
          arrowWidth: arrow.width, aligned: Math.abs(arrow.y + arrow.height / 2 - plus.y - plus.height / 2) < 1,
          fits: rect.right <= plus.left && row.scrollWidth <= row.clientWidth };
      });
      assert.equal(folder.title, "shared-amazon-appointment-system");
      assert.equal(folder.whitespace, "nowrap");
      assert.equal(folder.ellipsis, "ellipsis");
      if (width === 220) assert.equal(folder.truncated, true);
      assert.equal(folder.arrowWidth, 13);
      assert.ok(folder.aligned && folder.fits, JSON.stringify(folder));
    }
  }
  const first = rows.first().locator(".workspace-browser__session-open");
  const workspace = id => page.locator(`.workspace-browser__workspace[data-workspace-id="${id}"]`);
  const move = async (from, to, after = false) => {
    const target = workspace(to).locator(".workspace-browser__workspace-heading");
    const rect = await target.boundingBox();
    await workspace(from).locator(".workspace-browser__workspace-title").dragTo(target, { targetPosition: { x: 20, y: after ? rect.height - 2 : 2 } });
  };
  const order = () => page.locator(".workspace-browser__workspace").evaluateAll(elements => elements.map(el => el.dataset.workspaceId));
  await move("project-b", "project-a");
  assert.deepEqual(await order(), ["global", "project-b", "project-a"]);
  await move("project-b", "project-a", true);
  assert.deepEqual(await order(), ["global", "project-a", "project-b"]);
  assert.deepEqual(await page.evaluate(() => window.workspaceMoves), [["project-b", "project-a"], ["project-b", ""]]);
  await page.evaluate(() => { window.failWorkspaceMove = true; });
  await move("project-b", "project-a");
  await page.getByText("Workspace order could not be saved", { exact: true }).waitFor();
  assert.deepEqual(await order(), ["global", "project-a", "project-b"]);
  await page.evaluate(() => { window.failWorkspaceMove = false; });
  assert.equal(await first.getAttribute("aria-current"), "page");
  assert.match(await first.getAttribute("title"), /修复会话历史加载和跨工作区切换时的启动错误/);
  assert.equal(await rows.locator("small").count(), 0);
  await first.click();
  assert.deepEqual(await page.evaluate(() => window.sidebarCalls), ["session-0"]);
  await page.locator(".workspace-browser__show-more").click();
  assert.equal(await rows.count(), 7);
  await page.locator(".workspace-browser__search input").fill("实际请求 3");
  await page.waitForFunction(() => document.querySelectorAll(".workspace-browser__session").length === 1);
  assert.match(await rows.first().innerText(), /实际请求 3/);
  assert.equal(await workspace("project-a").locator(".workspace-browser__workspace-title").getAttribute("draggable"), "false");
  await rows.first().hover();
  await rows.first().locator(".workspace-browser__session-action").click();
  await page.waitForFunction(() => document.querySelectorAll(".workspace-browser__session").length === 0);
  assert.equal(await page.locator(".workspace-browser__archive-toggle").count(), 0);
  await page.evaluate(() => window.renderArchived());
  await rows.first().waitFor();
  await rows.first().hover();
  await rows.first().locator(".workspace-browser__session-action").click();
  await page.waitForFunction(() => document.querySelectorAll(".workspace-browser__session").length === 0);
  assert.deepEqual(errors, []);
  const integrated = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await integrated.goto(`${server.resolvedUrls.local[0]}?mock=bench&bench=1`);
  const sidebar = integrated.locator(".sidebar");
  const liveRow = sidebar.locator(".workspace-browser__session").first();
  await liveRow.waitFor();
  const sessionId = await liveRow.getAttribute("data-session-id");
  await liveRow.hover();
  await liveRow.locator(".workspace-browser__session-action").click();
  await integrated.waitForFunction(id => !document.querySelector(`.sidebar .workspace-browser__session[data-session-id="${id}"]`), sessionId);
  assert.equal(await sidebar.locator(".workspace-browser__archive-toggle").count(), 0);
  await sidebar.locator(".sidebar__utility-row").getByRole("button", { name: "Trash", exact: true }).click();
  await integrated.locator(".history-page").waitFor();
  await integrated.locator(".trash-page__sections").getByRole("button", { name: "Archived", exact: true }).click();
  const archivedRow = integrated.locator(`.trash-page__archived .workspace-browser__session[data-session-id="${sessionId}"]`);
  await archivedRow.waitFor();
  assert.equal(await integrated.locator(".history-clear:visible").count(), 0, "archive view has no clear-trash action");
  await archivedRow.hover();
  await archivedRow.locator(".workspace-browser__session-action").click();
  await archivedRow.waitFor({ state: "detached" });
  await sidebar.locator(`.workspace-browser__session[data-session-id="${sessionId}"]`).waitFor();
  await integrated.locator(".trash-page__sections").getByRole("button", { name: /Deleted/ }).click();
  await integrated.locator(".history-page").getByRole("button", { name: "Delete permanently", exact: true }).waitFor();
  console.log("PASS unified Trash entry: archive, restore to sidebar, switch back to deleted sessions");
  console.log("PASS compact sidebar: light/dark, 220/300px, title/time geometry, selection, open, expand, search, archive/restore");
} finally {
  await browser.close();
  await server.close();
}
