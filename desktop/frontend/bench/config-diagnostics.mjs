import assert from "node:assert/strict";
import { createServer } from "vite";
import { chromium } from "playwright";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const server = await createServer({ root, logLevel: "error", server: { host: "127.0.0.1", port: 0 } });
await server.listen();
const url = server.resolvedUrls.local[0];
console.log(`Configuration browser fixture: ${url}bench/config-diagnostics.html`);
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const page = await browser.newPage({ viewport: { width: 1100, height: 850 }, permissions: ["clipboard-read", "clipboard-write"] });
const errors = [];
page.on("pageerror", error => errors.push(error.message));
try {
  await page.goto(new URL("bench/config-diagnostics.html", url).href);
  for (const locale of ["en", "zh", "zh-TW"]) {
    await page.getByRole("button", { name: locale, exact: true }).click();
    await page.waitForFunction(locale => document.documentElement.lang === (locale === "zh" ? "zh-CN" : locale), locale);
    const settings = page.locator("section.config-compatibility");
    await settings.locator("summary").filter({ hasText: "39" }).waitFor();
    assert.equal(await page.locator(".banner").count(), 0, `${locale}: old rules have no chat banner`);
    assert.equal(await settings.locator("summary").count(), 1);
    await settings.locator("summary").click();
    await settings.locator("pre").first().waitFor();
    assert.equal(await settings.locator("pre").count(), 39);
    await settings.locator(".copybtn").click();
    assert.ok((await page.evaluate(() => navigator.clipboard.readText())).includes("example-0"));
    await settings.locator("details button.btn").click();
    await page.waitForFunction(() => window.configDiagnosticFixture.opened.includes("legacy-rules"));
    await page.setViewportSize({ width: 400, height: 850 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${locale}: long values wrap`);
    await settings.locator("summary").click();
    await settings.locator(":scope > button").click();
    await page.getByRole("button", { name: "broken", exact: true }).click();
    await page.locator(".banner summary").waitFor();
    await page.locator(".banner summary").click();
    await page.locator(".banner details button").last().click();
    await page.locator(".banner").waitFor({ state: "detached" });
    await page.getByRole("button", { name: "clean", exact: true }).click();
    await settings.locator('[role="status"]').waitFor();
    assert.equal(await settings.locator("summary").count(), 0);
    await page.getByRole("button", { name: "remote", exact: true }).click();
    await page.waitForFunction(() => { const text = document.querySelector("section.config-compatibility")?.textContent; return text?.includes("does not support") || text?.includes("不支持") || text?.includes("不支援"); });
    await page.getByRole("button", { name: "legacy", exact: true }).click();
    await page.setViewportSize({ width: 1100, height: 850 });
    console.log(`PASS ${locale}: grouped rules, details, copy, source, reload, error dismissal, project switch, old remote`);
    // Each language also starts a fresh renderer lifetime for dismissal tests.
    await page.reload();
  }
  assert.deepEqual(errors, []);
} finally { await browser.close(); await server.close(); }
