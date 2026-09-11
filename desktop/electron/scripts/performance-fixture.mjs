import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { build } from "esbuild";
import { _electron } from "playwright";

const root = resolve(import.meta.dirname, "..");

// An isolated native fixture, using production protocol, preload, diagnostic
// owner, Worker and frontend pressure monitor. No Go service or user data.
export async function performanceFixture(monitor = true) {
  const temp = mkdtempSync(join(tmpdir(), "reasonix-perf-"));
  let app;
  try {
    mkdirSync(join(temp, "assets"));
    writeFileSync(join(temp, "index.html"), '<!doctype html><title>Reasonix diagnostic fixture</title><button id="work">Work</button><pre id="output"></pre><script src="/assets/workload.js"></script>');
    await build({ bundle: true, platform: "browser", format: "iife", target: "chrome130", outfile: join(temp, "assets/workload.js"),
      define: { __BUILD_COMMIT__: '"diagnostic-fixture"', __BUILD_CHANNEL__: '"dev"', "import.meta.env.DEV": "false", "import.meta.env.MODE": '"production"' },
      stdin: { resolveDir: root, loader: "ts", contents: `
        import { installPerformancePressureMonitor } from '../frontend/src/lib/crash.ts';
        if (${monitor}) installPerformancePressureMonitor();
        function burn(ms) { const end = performance.now() + ms; while (performance.now() < end) Math.sqrt(Math.random()); }
        async function run(ms) {
          const times = []; let frames = 0; let last = performance.now(); const start = last;
          await new Promise(resolve => {
            function workFrame() {
              const now = performance.now(); times.push(now - last); last = now;
              let sum = 0; for (let i = 0; i < 100000; i++) sum += Math.sin(i + frames);
              document.getElementById('output').textContent = Array(200).fill(String(sum)).join('\\n');
              frames++; if (now - start < ms) requestAnimationFrame(workFrame); else resolve();
            }
            requestAnimationFrame(workFrame);
          });
          times.sort((a,b) => a-b);
          return { frames, elapsedMs: performance.now() - start, frameP95Ms: times[Math.floor(times.length * .95)], frameMaxMs: times[times.length - 1] };
        }
        window.diagnosticFixture = { run, burn };
        document.getElementById('work').onclick = () => burn(950);
      ` },
    });
    const common = { bundle: true, platform: "node", format: "cjs", external: ["electron"] };
    await build({ ...common, entryPoints: [join(root, "src/preload/index.ts")], outfile: join(temp, "preload.cjs") });
    await build({ ...common, entryPoints: [join(root, "src/main/profileAnalysisWorker.ts")], outfile: join(temp, "profile-analysis.cjs") });
    await build({ ...common, stdin: { resolveDir: root, contents: `
      import { app, BrowserWindow, protocol, ipcMain, net } from 'electron';
      import { registerAppProtocol } from './src/main/protocol.ts';
      import { registerRendererIpc } from './src/main/ipc.ts';
      import { ProcessDiagnostics } from './src/main/processDiagnostics.ts';
      import { createPerformanceHost } from './src/main/performanceHost.ts';
      app.setPath('userData', ${JSON.stringify(join(temp, "profile"))});
      protocol.registerSchemesAsPrivileged([{scheme:'reasonix', privileges:{standard:true,secure:true,supportFetchAPI:true,corsEnabled:false,stream:true}}]);
      app.whenReady().then(async () => {
        const log = {info(){},warn(){},error(){}};
        let copied = ''; const clipboard = {writeText:(text)=>{copied=text;},readText:()=>copied};
        registerAppProtocol({protocol,fetch:net.fetch,distRoot:${JSON.stringify(temp)},resources:()=>null,log});
        const win = new BrowserWindow({show:true,width:1000,height:750,webPreferences:{preload:${JSON.stringify(join(temp, "preload.cjs"))},sandbox:true,contextIsolation:true,nodeIntegration:false}});
        const diagnostics = new ProcessDiagnostics(()=>app.getAppMetrics(), undefined, ()=>win.isVisible()&&win.isFocused());
        if (${monitor}) { diagnostics.sample(); setInterval(()=>diagnostics.sample(),30000).unref(); }
        const performanceHost = createPerformanceHost({window:()=>win.isDestroyed()?null:win,workerPath:${JSON.stringify(join(temp, "profile-analysis.cjs"))},locale:()=>'en',dialog:{
          showMessageBox:async()=>({response:1}),showSaveDialog:async()=>({canceled:false,filePath:${JSON.stringify(join(temp, "fixture.heapsnapshot"))}})
        }});
        registerRendererIpc({ipcMain,contract:{protocolVersion:1,digest:'test',commands:[],commandSet:new Set()},
          window:{isTrustedSender:(sender,frame)=>sender===win.webContents && frame===sender.mainFrame},clipboard,
          serviceState:()=>({phase:'ready',generation:'test'}),processDiagnostics:()=>diagnostics.snapshot(),performance:performanceHost,log});
        app.once('will-quit',()=>performanceHost.dispose());
        await win.loadURL('reasonix://app/index.html'); win.focus();
      });
    ` }, outfile: join(temp, "main.cjs") });
    app = await _electron.launch({ args: [join(temp, "main.cjs")] });
    const page = await app.firstWindow();
    await page.waitForFunction(() => Boolean(window.diagnosticFixture && window.reasonixDesktop));
    await page.bringToFront();
    return { app, page, temp, async close() { await app.close(); rmSync(temp, { recursive: true, force: true }); } };
  } catch (error) {
    await app?.close();
    rmSync(temp, { recursive: true, force: true });
    throw error;
  }
}
