import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { readFile } from "node:fs/promises";
import { createServer } from "node:net";

const wrapper = resolve(dirname(fileURLToPath(import.meta.url)), "resource-run.mjs");
function runWrapper(args, env) {
  return new Promise((done, fail) => {
    const child = spawn(process.execPath, [wrapper,...args], {env:{...process.env,...env},stdio:["ignore","pipe","pipe"]});
    let out="",err="";
    child.stdout.on("data",x=>{out+=x});child.stderr.on("data",x=>{err+=x});
    child.once("error",fail);child.once("exit",code=>done({code,out,err}));
  });
}

test("shared policy/status and package-script admission preserve caps and exit status", async () => {
  const dir=await mkdtemp(join(tmpdir(),"reasonix-resource-policy-"));
  const env={REASONIX_RESOURCE_SCOPE:dir,CI:"false"};
  try {
    const configured=await runWrapper(["--configure","--cpu-budget","1","--memory-budget-mib","100","--cpu","1","--memory-mib","40"],env);
    assert.equal(configured.code,0,configured.err);
    assert.deepEqual(JSON.parse(configured.out).config,{budget:{cpu:1,memory_mib:100},task:{cpu:1,memory_mib:40}});
    // Run the actual shell-script path used by pnpm, including nested quotes,
    // chaining and failure propagation. No manual -- command wrapper is needed.
    const script=`"${process.execPath}" -e "console.log(process.env.VITEST_MAX_WORKERS)" && "${process.execPath}" -e "process.exit(7)"`;
    const ran=await runWrapper(["--script",script],env);
    assert.equal(ran.code,7,ran.err);assert.equal(ran.out.trim(),"1");
    const argv=await runWrapper(["--script",`"${process.execPath}" -e "console.log(JSON.stringify(process.argv.slice(1)))"`,"--","--pretty","a b","a&b",'a"b'],env);
    assert.equal(argv.code,0,argv.err);
    assert.deepEqual(JSON.parse(argv.out),["--pretty","a b","a&b",'a"b']);
    const status=await runWrapper(["--status"],env);
    assert.equal(status.code,0,status.err);
    assert.equal(JSON.parse(status.out).running,0);
    const oversized=await runWrapper(["--cpu","2","--",process.execPath,"-e","process.exit(0)"],env);
    assert.equal(oversized.code,1);assert.match(oversized.err,/fit budget/);
  } finally {await rm(dir,{recursive:true,force:true})}
});

test("standard pnpm and make entry points automatically join local admission", async () => {
  const root=resolve(dirname(wrapper),"..");
  for (const file of ["desktop/package.json","desktop/frontend/package.json","desktop/electron/package.json"]) {
    const pkg=JSON.parse(await readFile(join(root,file),"utf8"));
    for (const name of Object.keys(pkg.scripts).filter(name => ["build","typecheck","test","pretest"].includes(name) || name.startsWith("test:"))) {
      assert.match(pkg.scripts[name],/resource-run\.mjs --script /,`${file} ${name} bypassed admission`);
    }
  }
  const make=await readFile(join(root,"Makefile"),"utf8");
  for (const name of ["build","test","desktop-test","sdk-test"]) {
    const body=make.split(`\n${name}:\n`)[1]?.split(/\n[^\t\n]/)[0];
    assert.match(body,/\$\(RESOURCE_RUN\)/,`${name} bypassed admission`);
  }
});

test("cross-build environment cannot turn the native resource helper into a foreign executable", async () => {
  const result=await runWrapper(["--",process.execPath,"-e","console.log(process.env.GOOS+' '+process.env.GOARCH)"],{GOOS:"windows",GOARCH:"arm64"});
  assert.equal(result.code,0,result.err);
  assert.equal(result.out.trim(),"windows arm64");
});

test("Windows CI exercises owned Electron termination with its freshly built native service", async () => {
  const workflow=await readFile(resolve(dirname(wrapper),"../.github/workflows/ci.yml"),"utf8");
  const windows=workflow.match(/\n  desktop-windows:\n([\s\S]*?)(?=\n  [a-z][a-z0-9-]*:|$)/)?.[1];
  assert.ok(windows,"native Windows job missing");
  assert.match(windows,/REASONIX_DESKTOP_SERVICE: .*\/desktop\/build\/bin\/reasonix-desktop\.exe/);
  assert.match(windows,/run: pnpm --dir electron test:process-lifecycle/);
});

test("automatic CI scripts retain shell quoting without requiring the Go helper", async () => {
  const ran=await runWrapper(["--script",`"${process.execPath}" -e "console.log('first')" && "${process.execPath}" -e "console.log('second')"`],{CI:"true",PATH:dirname(process.execPath)});
  assert.equal(ran.code,0,ran.err);
  assert.deepEqual(ran.out.trim().split(/\r?\n/),["first","second"]);
});

test("resource wrapper preserves foreign cwd, worker caps and failing test status", async () => {
  const dir = await mkdtemp(join(tmpdir(), "reasonix-resource-cwd-"));
  try {
    const script = resolve(dirname(fileURLToPath(import.meta.url)), "resource-run.mjs");
    const child = spawn(process.execPath, [script, "--", process.execPath, "-e",
      "console.log(JSON.stringify({cwd:process.cwd(),workers:process.env.VITEST_MAX_WORKERS,go:process.env.GOFLAGS}));process.exit(7)"],
      { cwd: dir, stdio: ["ignore","pipe","pipe"] });
    let output = "", error = "";
    child.stdout.on("data", chunk => { output += chunk; });
    child.stderr.on("data", chunk => { error += chunk; });
    const code = await new Promise((done, fail) => { child.once("error", fail); child.once("exit", done); });
    assert.equal(code,7,error);
    const result = JSON.parse(output.trim());
    // macOS resolves /var to /private/var for process.cwd().
    assert.equal(result.cwd.replace(/^\/private/,""),dir.replace(/^\/private/,""));
    assert.ok(Number(result.workers) >= 1);
    assert.match(result.go, /-p=[12]/);
  } finally { await rm(dir,{recursive:true,force:true}); }
});

test("a successful check cannot leave a worker alive after releasing admission", async () => {
  let workerPID = 0;
  let workerSocket;
  let closed;
  const workerClosed = new Promise(resolveClose => { closed = resolveClose; });
  const server = createServer(socket => {
    workerSocket = socket;
    let input = "";
    socket.on("data", chunk => {
      input += String(chunk);
      if (/^\d+\n$/.test(input)) workerPID = Number(input.trim());
    });
    socket.once("close", closed);
  });
  await new Promise(resolveListen => server.listen(0, "127.0.0.1", resolveListen));
  try {
    const worker = "const s=require('net').connect("+server.address().port+",'127.0.0.1');s.on('connect',()=>{s.write(process.pid+'\\n');process.send('ready')});process.on('disconnect',()=>{});";
    const leader = "const c=require('child_process').spawn(process.execPath,['-e',"+JSON.stringify(worker)+"],{stdio:['ignore','ignore','ignore','ipc']});c.once('message',()=>process.exit(0));";
    const script = resolve(dirname(fileURLToPath(import.meta.url)), "resource-run.mjs");
    const child = spawn(process.execPath,[script,"--",process.execPath,"-e",leader],{stdio:"ignore"});
    const code = await new Promise((done,fail) => { child.once("error",fail); child.once("exit",done); });
    assert.equal(code,0);
    assert.ok(workerPID > 0);
    let timer;
    try {
      await Promise.race([workerClosed,new Promise((_,reject) => { timer=setTimeout(()=>reject(new Error("successful leader leaked its worker")),5000); })]);
    } finally { clearTimeout(timer); }
  } finally {
    if (workerPID && workerSocket && !workerSocket.destroyed) { try { process.kill(workerPID,"SIGKILL"); } catch {} }
    workerSocket?.destroy();
    server.close();
  }
});
