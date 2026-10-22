// A project's context menu stays inside a window that is shorter than the menu: it scrolls, so every action
// below the fold can still be reached.
import { chromium } from "playwright";

const BASE = process.env.PERF_URL ?? "http://localhost:4399/perf.html?pref=zh&ws=3&sess=3&turns=2";
const fails = [];
const check = (name, ok, detail = "") => {
  console.log(`${ok ? "  ok" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!ok) fails.push(name);
};

const browser = await chromium.launch();
try {
  for (const scheme of ["light", "dark"]) {
    for (const height of [140, 300, 900]) {
      const page = await browser.newPage({ locale: "zh-CN", colorScheme: scheme, viewport: { width: 1280, height }, reducedMotion: "reduce" });
      await page.goto(BASE, { waitUntil: "networkidle" });
      const rows = page.locator(".wsrow");
      await rows.first().waitFor();
      await rows.first().click({ button: "right" });
      const menu = page.locator(".session-pop").first();
      await menu.waitFor();
      const box = await menu.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return { top: r.top, bottom: r.bottom, scroll: el.scrollHeight > el.clientHeight, inner: innerHeight };
      });
      const tag = `${scheme} ${height}px`;
      check(`${tag}: the menu fits the window`, box.top >= 0 && box.bottom <= box.inner, `top ${Math.round(box.top)}, bottom ${Math.round(box.bottom)}, window ${box.inner}`);
      const last = menu.locator("[role=menuitem]").last();
      await last.scrollIntoViewIfNeeded();
      const reach = await last.evaluate((el) => { const r = el.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight; });
      check(`${tag}: the last action can be reached`, reach, box.scroll ? "scrolls" : "fits whole");
      await page.close();
    }
  }
} finally {
  await browser.close();
}
if (fails.length) {
  console.error(`\n${fails.length} failed: ${fails.join("; ")}`);
  process.exit(1);
}
console.log("\nall passed");
