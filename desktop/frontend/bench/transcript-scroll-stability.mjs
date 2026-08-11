#!/usr/bin/env node

import { spawn } from "node:child_process";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(frontendDir, ".pw-browsers")
  : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");
const port = Number(process.env.REASONIX_TRANSCRIPT_SCROLL_PORT ?? 4619);
const url = `http://127.0.0.1:${port}/?mock=bench&bench=1`;

function assert(condition, message) {
  if (!condition) throw new Error(message);
  process.stdout.write(`  PASS  ${message}\n`);
}

async function waitForServer() {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const ready = await new Promise((resolve) => {
      const request = http.get(url, (response) => {
        response.resume();
        resolve((response.statusCode ?? 500) < 500);
      });
      request.on("error", () => resolve(false));
    });
    if (ready) return;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error("transcript scroll preview did not become ready");
}

const preview = spawn("pnpm", ["exec", "vite", "preview", "--port", String(port), "--strictPort", "--host", "127.0.0.1"], {
  cwd: frontendDir,
  stdio: "ignore",
});

let browser;
try {
  await waitForServer();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => document.querySelectorAll(".transcript__row").length > 4, undefined, { timeout: 30_000 });
  await page.waitForFunction(() => !document.querySelector(".startup-splash"), undefined, { timeout: 30_000 });
  await page.click('.project-tree__topic-main:has-text("bench:tools-38t")');
  await page.waitForFunction(() => document.querySelector(".transcript")?.textContent?.includes("pkg-41/mod.go"), undefined, { timeout: 30_000 });

  await page.evaluate(() => {
    const transcript = document.querySelector(".transcript");
    if (!transcript) return;
    transcript.scrollTop = Math.max(0, transcript.scrollHeight - transcript.clientHeight * 2);
    window.__scrollWrites = [];
    window.__scrollGestureTrace = [];
    window.__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (owner, top) => window.__scrollWrites.push({ owner, top });
    new MutationObserver(() => {
      window.__scrollGestureTrace.push(transcript.dataset.scrollGesture ?? "idle");
    }).observe(transcript, { attributes: true, attributeFilter: ["data-scroll-gesture"] });
  });

  const box = await page.locator(".transcript").boundingBox();
  assert(box != null, "bench exposes the transcript viewport");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -420);
  await page.waitForFunction(() => window.__scrollGestureTrace?.includes("wheel"), undefined, { timeout: 3_000 });
  assert(true, "real Chromium wheel input enters the user scroll session");

  const midGesture = await page.evaluate(() => {
    const transcript = document.querySelector(".transcript");
    if (!transcript) return null;
    window.__scrollWrites = [];
    transcript.dispatchEvent(new WheelEvent("wheel", { deltaY: -240, bubbles: true, cancelable: true }));
    transcript.scrollTop = Math.max(0, transcript.scrollTop - 240);
    transcript.dispatchEvent(new Event("scroll"));
    const row = [...transcript.querySelectorAll(".transcript__row")].find((candidate) => {
      const rect = candidate.getBoundingClientRect();
      return rect.bottom > transcript.getBoundingClientRect().top;
    });
    if (row instanceof HTMLElement) row.style.paddingTop = "180px";
    return { gesture: transcript.dataset.scrollGesture, top: transcript.scrollTop, rowKey: row?.dataset.rowKey ?? null };
  });
  assert(midGesture?.gesture === "wheel", "variable-height mutation occurs while wheel ownership is active");
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const during = await page.evaluate(() => ({
    writes: window.__scrollWrites ?? [],
    gesture: document.querySelector(".transcript")?.dataset.scrollGesture,
  }));
  assert(during.writes.every((write) => !["virtualizer", "stream", "container-resize", "footer-resize"].includes(write.owner)),
    `compensating owners stay silent during variable-height scroll (${JSON.stringify(during.writes)})`);

  await page.evaluate(() => document.querySelector(".transcript")?.dispatchEvent(new Event("scrollend")));
  await page.waitForFunction(() => !document.querySelector(".transcript")?.dataset.scrollGesture);
  const settledTop = await page.locator(".transcript").evaluate((element) => element.scrollTop);
  await page.waitForTimeout(260);
  const lateTop = await page.locator(".transcript").evaluate((element) => element.scrollTop);
  assert(Math.abs(lateTop - settledTop) <= 1, `scrollend has no delayed full-measure jump (${settledTop} → ${lateTop})`);

  const middle = await page.evaluate(() => {
    const transcript = document.querySelector(".transcript");
    if (!transcript) return null;
    window.__scrollWrites = [];
    transcript.dispatchEvent(new PointerEvent("pointerdown", { button: 1, buttons: 4, pointerId: 9, bubbles: true }));
    const started = transcript.dataset.scrollGesture;
    transcript.scrollTop = Math.max(0, transcript.scrollTop - 160);
    transcript.dispatchEvent(new Event("scroll"));
    return { started, continued: transcript.dataset.scrollGesture, top: transcript.scrollTop };
  });
  assert(middle?.started === "middle-button", "middle-button activation owns Windows auto-scroll");
  assert(middle?.continued === "middle-button", "unowned native scroll refreshes middle-button ownership");
  const middleWrites = await page.evaluate(() => window.__scrollWrites ?? []);
  assert(middleWrites.length === 0, "middle-button auto-scroll admits no compensating programmatic writes");
  await page.evaluate(() => document.querySelector(".transcript")?.dispatchEvent(new Event("scrollend")));
  await page.waitForFunction(() => !document.querySelector(".transcript")?.dataset.scrollGesture);
  assert(true, "middle-button session terminates on native scrollend");

  process.stdout.write("\ntranscript scroll stability browser gate passed\n");
} finally {
  await browser?.close();
  preview.kill("SIGTERM");
}
