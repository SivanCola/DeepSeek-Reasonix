import assert from "node:assert/strict";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build, loadConfigFromFile, preview } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(root, ".pw-browsers") : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");
const output = await mkdtemp(path.join(tmpdir(), "reasonix-layout-build-"));
const evidence = process.env.REASONIX_LAYOUT_ARTIFACTS;
const samples = [];
let server;
let browser;
try {
  const loaded = await loadConfigFromFile({ command: "build", mode: "production" }, path.join(root, "vite.config.ts"));
  // Build the real component fixture separately; do not alter the application's
  // dist, sourcemap archive or placeholder while running a regression test.
  const config = loaded.config;
  await build({ ...config, configFile: false, root, logLevel: "error",
    plugins: config.plugins.filter(plugin => !["archive-hidden-sourcemaps", "keep-dist-placeholder"].includes(plugin?.name)),
    build: { ...config.build, outDir: output, minify: false, sourcemap: false,
      rolldownOptions: { ...config.build.rolldownOptions, input: path.join(root, "bench/transcript-layout.html") } },
  });
  server = await preview({ configFile: false, root, logLevel: "error", build: { outDir: output },
    preview: { host: "127.0.0.1", port: 0 } });
  const address = server.httpServer.address();
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) });
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
  const errors = [];
  page.on("pageerror", error => { errors.push(error.message); console.error(error.message); });
  await page.goto(`http://127.0.0.1:${address.port}/bench/transcript-layout.html`);
  await page.waitForSelector(".transcript__row .code").catch(async error => {
    console.error((await page.locator("body").innerText()).slice(0, 1200));
    throw error;
  });
  await page.evaluate(() => document.fonts.ready);
  const configure = async options => {
    const revision = await page.evaluate(options => {
      const fixture = window.transcriptLayoutFixture;
      const next = fixture.revision + 1;
      fixture.configure(options);
      return next;
    }, options);
    await page.waitForFunction(revision => window.transcriptLayoutFixture.revision === revision, revision);
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  const measure = async label => {
    const sample = await page.evaluate(() => {
      const rect = element => { const r = element.getBoundingClientRect(); return { left: r.left, right: r.right, width: r.width }; };
      const chat = document.querySelector(".chat-pane");
      const content = document.querySelector(".transcript-navigation-content");
      const transcript = document.querySelector(".transcript");
      const launcher = document.querySelector(".dock-launcher");
      return { viewport: innerWidth, chat: rect(chat), content: rect(content),
        clientWidth: transcript.clientWidth, scrollWidth: transcript.scrollWidth,
        userBubbles: [...document.querySelectorAll(".msg--user .msg__body")].map(rect),
        launcher: launcher ? rect(launcher) : null };
    });
    samples.push({ label, ...sample });
    assert.ok(sample.content.right <= sample.chat.right + 1, label + ": content column fits chat");
    assert.ok(sample.scrollWidth <= sample.clientWidth + 1, label + ": transcript has no horizontal overflow");
    for (const bubble of sample.userBubbles) {
      assert.ok(bubble.left >= sample.content.left - 1 && bubble.right <= sample.content.right + 1, label + ": complete user bubble fits");
    }
    if (sample.launcher) assert.ok(sample.launcher.right <= sample.chat.right + 1, label + ": launcher fits");
    return sample;
  };
  // This first case is the original full-width disappearing-message regression.
  await measure("full-width long code");
  for (const layout of ["creation", "workbench"]) {
    for (const width of ["standard", "full"]) {
      for (const viewport of [760, 820, 1000, 1280, 1920]) {
        await page.setViewportSize({ width: viewport, height: 1080 });
        await configure({ layout, width, sidebar: true, dock: false, launcher: false });
        await measure(`${layout}/${width}/${viewport}`);
      }
    }
  }
  await page.setViewportSize({ width: 1920, height: 1080 });
  for (const sidebar of [false, true]) for (const dock of [false, true]) {
    await configure({ sidebar, dock, launcher: true });
    await measure(`sidebar=${sidebar}/dock=${dock}`);
  }
  await configure({ layout: "creation", sidebar: false, dock: false, launcher: true });
  for (const width of [1009, 1010, 1011]) {
    await page.setViewportSize({ width, height: 1080 });
    await page.waitForFunction(hidden => Boolean(document.querySelector(".dock-launcher")) !== hidden, width < 1010);
    await measure("launcher threshold " + width);
  }
  await configure({ launcher: false });
  for (let index = 0; index < 4; index++) {
    await configure({ long: index % 2 === 0 });
    await measure("content replacement " + index);
  }
  await configure({ turns: 120, long: true });
  await page.waitForSelector('[data-transcript-render-mode="windowed"]');
  await measure("windowed long session");
  await configure({ turns: 1, text: "prefix ", streaming: true });
  await page.waitForSelector(".msg--assistant .md");
  const prose = "prefix " + "abcdefghij".repeat(60);
  await configure({ text: prose });
  await page.waitForSelector(".md--stream-tail");
  await measure("streaming long prose");
  await configure({ streaming: false });
  await page.waitForSelector(".md[data-markdown-blocks] p");
  await measure("completed long prose");
  const paragraph = await page.locator(".msg--assistant .md p").evaluate(element => ({
    width: element.clientWidth, scrollWidth: element.scrollWidth, text: element.textContent,
  }));
  assert.ok(paragraph.scrollWidth <= paragraph.width + 1, "completed prose wraps inside its own column");
  assert.equal(paragraph.text, prose, "wrapping never changes source text");
  await page.reload();
  await page.waitForSelector(".transcript__row .code");
  await configure({ turns: 1, text: prose });
  await page.waitForSelector(".md[data-markdown-blocks] p");
  await measure("fresh history long prose");
  await configure({ text: "| Key | Value |\n|---|---|\n| " + "abcdefghij".repeat(60) + " | data |\n\n$$\\sum_{n=1}^{10}n$$\n\n\`\`\`text\n" + "abcdefghij".repeat(60) + "\n\`\`\`" });
  await page.waitForSelector(".md .katex");
  await measure("wide table and math");
  const code = await page.locator(".md pre").first().evaluate(element => ({
    width: element.clientWidth, scrollWidth: element.scrollWidth, whitespace: getComputedStyle(element).whiteSpace,
  }));
  assert.equal(code.whitespace, "pre", "ordinary code retains its original lines");
  assert.ok(code.scrollWidth > code.width, "long code remains locally scrollable");
  assert.equal(await page.locator(".katex").first().evaluate(element => getComputedStyle(element).overflowWrap), "normal");
  assert.deepEqual(errors, []);
  console.log(`PASS transcript width: ${samples.length} geometry scenarios`);
  if (evidence) { await mkdir(evidence, { recursive: true }); await page.screenshot({ path: path.join(evidence, "layout.png") }); }
} finally {
  if (evidence) { await mkdir(evidence, { recursive: true }); await writeFile(path.join(evidence, "layout.json"), JSON.stringify(samples, null, 2)); }
  await browser?.close();
  await server?.httpServer.close();
  await rm(output, { recursive: true, force: true });
}
