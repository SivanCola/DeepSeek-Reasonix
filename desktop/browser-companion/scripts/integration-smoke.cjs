// Real-Electron integration smoke: drives the companion through its PRODUCTION
// dispatch path (CompanionApp.handle — the same method the host drives over
// stdin/stdout), inside a real Electron main process, and verifies the human
// browsing chain: chrome assets load from dist, the renderer script binds the
// toolbar, a plain tab.open binds the chrome to the chat (no separate
// owner.activate needed), the tab strip state follows every command, one
// active tab per owner holds even for two foreground opens, cross-owner
// visibility isolates chats, closing a tab detaches its view, and the agent
// badge reflects the real lease.
//
// Run with `pnpm test:integration` (builds first).

const { app } = require("electron");
const http = require("node:http");
const { CompanionApp } = require("../dist/app.js");

const results = [];
let fatal = false;
function check(label, cond, detail) {
  results.push({ label, ok: !!cond, detail: detail ?? "" });
  // stderr: stdout is not drained reliably before app.exit in Electron.
  console.error(`${cond ? "PASS" : "FAIL"}  ${label}${cond ? "" : "  " + (detail ?? "")}`);
}
function fatalError(label, err) {
  fatal = true;
  results.push({ label, ok: false, detail: err && err.message ? err.message : String(err) });
  console.error(`FAIL  ${label}  ${err && err.stack ? err.stack : err}`);
}

let server;
const pageHtml = "<html><head><title>Test Page</title></head><body>hello</body></html>";

const PROTOCOL = 1;
function req(requestId, method, ownerId, params) {
  return { protocolVersion: PROTOCOL, requestId, ownerId, method, params: params ?? {} };
}

app.whenReady().then(async () => {
  process.on("unhandledRejection", (e) => console.error("UNHANDLED:", e && e.stack ? e.stack : e));
  try {
  server = http.createServer((req, res) => {
    if (req.url === "/download") {
      res.setHeader("Content-Type", "text/plain");
      res.setHeader("Content-Disposition", 'attachment; filename="f.txt"');
      res.end("data");
      return;
    }
    res.setHeader("Content-Type", "text/html");
    res.end(pageHtml);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${server.address().port}/`;

  const events = [];
  companion = new CompanionApp({
    componentVersion: "43.3.0-r1-test",
    electronVersion: "test",
    chromiumVersion: "test",
    pid: 1234,
    emitEvent: (name, ownerId, data) => events.push({ name, ownerId, data }),
    exit: (code) => {
      // Record only: calling app.exit here hangs this environment and blocks
      // the event loop, which would prevent finishTest's SIGKILL backstop.
      app.exitCode = code;
    },
  });
  companion.markReady();

  // A tab operation before hello (window not open yet) is refused with the
  // protocol's not_ready code.
  const early = companion.handle(req("r-early", "tab.open", "ownerX", { ownerId: "ownerX", url, disposition: "foreground" }));
  check("tab.open before hello is not_ready", early.error && early.error.code === "not_ready", JSON.stringify(early));

  // hello opens the window (the same order a real host drives).
  const hello = companion.handle(req("r-hello", "hello", "", { hostName: "test", hostVersion: "test" }));
  check("hello responds with identity", hello.result && hello.result.componentVersion === "43.3.0-r1-test", JSON.stringify(hello));
  check("hello opened the window", !!companion.chrome, "chrome is null");

  // The renderer sends requestState during page load, so this probe must be
  // registered right after the window opens (before the load completes).
  const rendererLive = new Promise((resolve) => {
    const wc = companion.chrome.view.webContents;
    const onIpc = (_event, channel, ...args) => {
      if (channel === "reasonix-chrome" && args[0] && args[0].kind === "requestState") {
        wc.removeListener("ipc-message", onIpc);
        resolve(true);
      }
    };
    wc.on("ipc-message", onIpc);
    setTimeout(() => resolve("renderer never reported"), 8000);
  });

  // 1. Chrome assets load from the packaged dist.
  const chromeLoaded = await new Promise((resolve) => {
    const wc = companion.chrome.view.webContents;
    wc.once("did-finish-load", () => resolve(true));
    wc.once("did-fail-load", (_e, code, desc) => resolve(`load failed: ${code} ${desc}`));
    setTimeout(() => resolve("load timed out"), 8000);
  });
  check("chrome assets load from dist", chromeLoaded === true, String(chromeLoaded));

  check("chrome preload bridge + renderer script live", (await rendererLive) === true, String(await rendererLive));

  // 2. A PLAIN chat-link path: tab.open only (no owner.activate, no
  //    window.focus). The chrome must bind to the opened chat.
  const openA = companion.handle(req("r-a1", "tab.open", "ownerA", { ownerId: "ownerA", url, disposition: "foreground" }));
  check("tab.open responds", openA.result && openA.result.tabId, JSON.stringify(openA));
  const snap1 = companion.chromeSnapshot();
  check("chrome bound to the opened chat (no owner.activate needed)", snap1.tabs.length === 1 && snap1.tabs[0].url === url, JSON.stringify(snap1));
  check("visible owner is ownerA", companion.activeOwnerId === "ownerA", companion.activeOwnerId);

  // 3. Two FOREGROUND opens of the same chat: only the second stays active.
  const a2 = companion.handle(req("r-a2", "tab.open", "ownerA", { ownerId: "ownerA", url, disposition: "foreground" }));
  check("second foreground tab opens", !!a2.result && a2.result.tabId !== openA.result.tabId, JSON.stringify(a2));
  const activeA = companion.tabs.list("ownerA").filter((t) => t.active).map((t) => t.id);
  check("single active after two foreground opens", activeA.length === 1 && activeA[0] === a2.result.tabId, `active=${activeA}`);

  // 4. Cross-owner isolation: opening ownerB's foreground tab binds the
  //    chrome to ownerB and hides ownerA's pages.
  const openB = companion.handle(req("r-b1", "tab.open", "ownerB", { ownerId: "ownerB", url, disposition: "foreground" }));
  const vA = companion.tabs.webContentsFor(openA.result.tabId);
  const vB = companion.tabs.webContentsFor(openB.result.tabId);
  const snapB = companion.chromeSnapshot();
  check("chrome switched to ownerB", snapB.tabs.length === 1 && snapB.tabs[0].ownerId === "ownerB", JSON.stringify(snapB));
  check("ownerB's page visible", vB.getVisible() === true, `vB=${vB.getVisible()}`);
  check("ownerA's page hidden", vA.getVisible() === false, `vA=${vA.getVisible()}`);

  // 4b. The host-driven tab.navigate path also refreshes the chrome state.
  companion.handle(req("r-nav", "tab.navigate", "ownerA", { ownerId: "ownerA", tabId: a2.result.tabId, url: url + "?host" }));
  check("host tab.navigate updates the tab state", companion.tabs.list("ownerA").find((t) => t.id === a2.result.tabId).url === url + "?host", companion.tabs.list("ownerA").map((t) => t.url).join(","));

  // 4c. permissions.list is an empty array (host-side array contract).
  const grants = companion.handle(req("r-grants", "permissions.list", "", {}));
  check("permissions.list returns an empty grants array", Array.isArray(grants.result.grants) && grants.result.grants.length === 0, JSON.stringify(grants));

  // 4d. (Download attribution is wired in tabs.ts via the will-download
  //     third argument, but downloads do not fire in this headless test
  //     environment — verified by the Go-side real-Electron smoke.)

  // 4e. window.open with an owner binds the chrome to that chat.
  companion.handle(req("r-wo", "window.open", "ownerD", { ownerId: "ownerD" }));
  check("window.open binds the visible owner", companion.activeOwnerId === "ownerD", companion.activeOwnerId);

  // 4f. tab.act is refused with the typed unsupported error until the CDP
  //     phase lands; data.clear reports the cleared scopes.
  const act = companion.handle(req("r-act2", "tab.act", "ownerA", { ownerId: "ownerA", tabId: a2.result.tabId, action: "click" }));
  check("tab.act is typed unsupported", act.error && act.error.code === "unsupported", JSON.stringify(act));
  const clear = companion.handle(req("r-clear", "data.clear", "", { scopes: ["cache"] }));
  check("data.clear reports cleared scopes", Array.isArray(clear.result.cleared) && clear.result.cleared.includes("cache"), JSON.stringify(clear));

  // 5. The address bar (chrome navigate) acts on the visible owner's active
  //    tab through the production command path.
  companion.handle(req("r-act", "owner.activate", "ownerA", { ownerId: "ownerA" }));
  companion.chromeNavigate(url + "?second");
  check("navigate command targets the visible owner's active tab", companion.tabs.list("ownerA").find((t) => t.active).url === url + "?second");

  // 6. Chrome commands keep the tab strip state fresh (command -> pushState).
  //    A send spy proves the state is PUBLISHED to the renderer, not just
  //    computed: removing every pushState() call fails this check.
  let sends = 0;
  const cmdWc = companion.chrome.view.webContents;
  const origSend = cmdWc.send.bind(cmdWc);
  cmdWc.send = (...args) => {
    sends += 1;
    return origSend(...args);
  };
  const sendsBefore = sends;
  const tabsBefore = companion.chromeSnapshot().tabs.length;
  companion.chromeCloseTab(openA.result.tabId);
  check("close command publishes state to the renderer", sends > sendsBefore, `sends ${sendsBefore}->${sends}`);
  check("close command updates the tab strip state", companion.chromeSnapshot().tabs.length === tabsBefore - 1, `before=${tabsBefore} after=${companion.chromeSnapshot().tabs.length}`);
  const tabAfterClose = companion.chromeSnapshot().tabs[0];
  companion.chromeNewTab();
  check("new-tab command adds a blank tab", companion.chromeSnapshot().tabs.length === 2, `count=${companion.chromeSnapshot().tabs.length}`);
  companion.chromeActivateTab(tabAfterClose.id);
  check("activate command updates the active tab", companion.tabs.list("ownerA").find((t) => t.active).id === tabAfterClose.id);

  // 7. Agent badge reflects the real lease, and lease changes refresh it.
  check("badge off without lease", companion.chromeSnapshot().agentControlling === false);
  companion.tabs.acquireLease("ownerA", tabAfterClose.id);
  check("badge on with lease", companion.chromeSnapshot().agentControlling === true);
  companion.chromeTakeover();
  check("takeover command revokes and refreshes the badge", companion.chromeSnapshot().agentControlling === false);
  check("takeover emitted the agent.takeover event", events.some((e) => e.name === "agent.takeover"), JSON.stringify(events));

  // 8. Closing tabs detaches their views from the window.
  companion.chromeCloseTab(tabAfterClose.id);
  check("closed tab detached from window", companion.window.contentView.children.length === 3, `children=${companion.window.contentView.children.length}`);

  // 9. First tab without any visible owner binds the chrome even in
  //    background (the very first open cannot stay hidden).
  companion.tabs.setActiveOwner("");
  companion.handle(req("r-c1", "tab.open", "ownerC", { ownerId: "ownerC", url, disposition: "background" }));
  check("first-ever tab binds the chrome even in background", companion.activeOwnerId === "ownerC", companion.activeOwnerId);

  // 10. owner.remove cleans the owner's tabs and resets the visible owner.
  companion.handle(req("r-del", "owner.remove", "ownerC", { ownerId: "ownerC" }));
  check("owner.remove clears the owner's tabs", companion.tabs.list("ownerC").length === 0, `count=${companion.tabs.list("ownerC").length}`);
  check("owner.remove resets the visible owner", companion.activeOwnerId === "", companion.activeOwnerId);
  // Chrome commands with no visible owner are silent, not throwing.
  companion.chromeActivateTab("ghost");
  companion.chromeCloseTab("ghost");
  check("chrome commands are silent without a visible owner", true);

  // 11. New Tab + address bar: the blank tab is the visible owner's active
  //     tab, so an address-bar navigation lands on it.
  companion.chromeNewTab();
  companion.chromeNavigate(url + "?blank");
  const blankTabs = companion.chromeSnapshot().tabs;
  check("address bar navigates the new blank tab", blankTabs.length === 1 && blankTabs[0].url === url + "?blank", JSON.stringify(blankTabs));

  // 13. hello-before-ready ordering: the handshake succeeds without opening
  //     the window, tab.open is refused with not_ready, and markReady opens
  //     the window so a host restoring right after hello never sees
  //     not_ready tab.open responses.
  {
    const early = new CompanionApp({
      componentVersion: "early-test",
      electronVersion: "test",
      chromiumVersion: "test",
      pid: 5678,
      emitEvent: () => {},
      exit: () => {},
    });
    const h0 = early.handle(req("r-h0", "hello", "", { hostName: "test", hostVersion: "test" }));
    check("hello before ready succeeds without a window", !!h0.result && !early.chrome, JSON.stringify(h0));
    const t0 = early.handle(req("r-t0", "tab.open", "ownerX", { ownerId: "ownerX", url, disposition: "foreground" }));
    check("tab.open before ready is not_ready", t0.error && t0.error.code === "not_ready", JSON.stringify(t0));
    early.markReady();
    check("markReady after hello opens the window", !!early.chrome, "chrome is null");
    const t1 = early.handle(req("r-t1", "tab.open", "ownerX", { ownerId: "ownerX", url, disposition: "foreground" }));
    check("tab.open after ready succeeds", !!t1.result && !!t1.result.tabId, JSON.stringify(t1));
    early.window.destroy();
  }

  } catch (e) {
    // A fatal exception must fail the run: an aborted script must never
    // produce a passing N/N summary.
    fatalError("fatal test error", e);
  }
  // Note: the window.close -> process-exit path is verified by the Go-side
  // real-Electron smoke (window.close, 8/8); this environment's 'closed'
  // event does not fire reliably, and full destroyAll hangs it. Per-tab view
  // destruction is covered by the closeTab checks above.

  const failed = results.filter((r) => !r.ok).length;
  console.error(`\n${results.length - failed}/${results.length} passed`);
  // Machine-readable completion marker the wrapper validates: FATAL when a
  // fatal exception aborted the run, otherwise PASS/FAIL with the counts.
  console.error(
    fatal
      ? "RESULT: FATAL"
      : failed === 0
        ? `RESULT: PASS ${results.length}/${results.length}`
        : `RESULT: FAIL ${results.length - failed}/${results.length}`,
  );
  companion.window.destroy();
  server.close();
  finishTest(fatal || failed > 0 ? 1 : 0);
});

function finishTest(code) {
  // This environment's Electron never exits cleanly (app.exit, app.quit and
  // process.exit all hang); stderr is synchronous and the wrapper streams it
  // live, so the result is already observable before SIGKILL. The wrapper
  // maps the output to the real exit code.
  setTimeout(() => process.kill(process.pid, "SIGKILL"), 200);
}

setTimeout(() => {
  console.error("integration smoke timed out");
  finishTest(1);
}, 30000);
