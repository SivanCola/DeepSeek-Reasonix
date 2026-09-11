// Native diagnostic contract smoke; uses a disposable document and no Go service/user data.
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { build } from "esbuild";
import { _electron } from "playwright";

const root = resolve(import.meta.dirname, "..");
const temp = mkdtempSync(join(tmpdir(), "reasonix-perf-smoke-"));
let app;
try {
  writeFileSync(join(temp, "index.html"), "<!doctype html><title>Diagnostic smoke</title>");
  const common = { bundle: true, platform: "node", format: "cjs", external: ["electron"] };
  await build({ ...common, entryPoints: [join(root, "src/preload/index.ts")], outfile: join(temp, "preload.cjs") });
  await build({ ...common, stdin: { resolveDir: root, contents: `
    import { app, BrowserWindow, protocol, ipcMain, net } from 'electron';
    import { registerAppProtocol } from './src/main/protocol.ts';
    import { registerRendererIpc } from './src/main/ipc.ts';
    import { ProcessDiagnostics } from './src/main/processDiagnostics.ts';
    app.setPath('userData', ${JSON.stringify(join(temp, "profile"))});
    protocol.registerSchemesAsPrivileged([{scheme:'reasonix', privileges:{standard:true,secure:true,supportFetchAPI:true}}]);
    app.whenReady().then(async () => {
      const log = {info(){},warn(){},error(){}};
      registerAppProtocol({protocol,fetch:net.fetch,distRoot:${JSON.stringify(temp)},resources:()=>null,log});
      const win = new BrowserWindow({show:false,webPreferences:{preload:${JSON.stringify(join(temp, "preload.cjs"))},sandbox:true,contextIsolation:true,nodeIntegration:false}});
      const diagnostics = new ProcessDiagnostics(()=>app.getAppMetrics());
      diagnostics.sample();
      registerRendererIpc({ipcMain,contract:{protocolVersion:1,digest:'test',commands:[]},
        window:{isTrustedSender:(sender,frame)=>sender===win.webContents && frame===sender.mainFrame},
        serviceState:()=>({phase:'ready',generation:'test'}),processDiagnostics:()=>diagnostics.snapshot(),log});
      await win.loadURL('reasonix://app/index.html');
    });
  ` }, outfile: join(temp, "main.cjs") });
  app = await _electron.launch({ args: [join(temp, "main.cjs")] });
  const page = await app.firstWindow();
  await page.waitForFunction(() => Boolean(window.reasonixDesktop));
  const result = await page.evaluate(async () => {
    const profiler = new Profiler({ sampleInterval: 10, maxBufferSize: 1000 });
    const end = performance.now() + 150;
    while (performance.now() < end) Math.sqrt(Math.random());
    const trace = await profiler.stop();
    return {
      samples: trace.samples.length,
      policy: (await fetch(location.href)).headers.get('Document-Policy'),
      processes: await window.reasonixDesktop.native.processDiagnostics(),
    };
  });
  assert.equal(result.policy, "js-profiling");
  assert.ok(result.samples > 0, "native profiler collected samples");
  assert.equal(result.processes.scope, "electron");
  assert.ok(result.processes.samples[0].processes.some((p) => p.type === "Browser" && p.pid > 0));
  console.log(`PASS native profiler (${result.samples} samples), policy and process IPC`);
} finally {
  await app?.close();
  rmSync(temp, { recursive: true, force: true });
}
