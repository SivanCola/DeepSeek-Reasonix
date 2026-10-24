// The settings menu is short enough to show whole on a 1080p window, and each of its icons is drawn centred in
// its box, so none of them sits above the label it belongs to.
import { chromium } from "playwright";

const BASE = process.env.PERF_URL ?? "http://localhost:4399/perf.html?pref=zh&ws=1&sess=1&turns=1";
const fails = [];
const check = (name, ok, detail = "") => {
  console.log(`${ok ? "  ok" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!ok) fails.push(name);
};

const browser = await chromium.launch();
try {
  for (const scheme of ["light", "dark"]) {
    const ctx = await browser.newContext({ locale: "zh-CN", colorScheme: scheme, viewport: { width: 1920, height: 1032 }, reducedMotion: "reduce" });
    const page = await ctx.newPage();
    await page.goto(BASE, { waitUntil: "networkidle" });
    await page.waitForSelector(".app");
    await page.evaluate(() => document.querySelector('[data-action="chrome.settings"]')?.click());
    await page.waitForSelector(".prefs-nav");
    await page.waitForTimeout(600);
    const nav = await page.evaluate(() => {
      const el = document.querySelector(".prefs-nav");
      return { need: el.scrollHeight, have: el.clientHeight };
    });
    check(`${scheme}: the whole menu shows without a scrollbar`, nav.need <= nav.have, `${nav.need}px needed, ${nav.have}px shown`);
    const icons = await page.evaluate(() =>
      [...document.querySelectorAll('.prefs-nav button[data-action="settings.section"] svg')].map((svg) => {
        const box = svg.viewBox.baseVal;
        const bb = svg.getBBox();
        return { name: svg.closest("button").textContent.trim().slice(0, 6), off: bb.y + bb.height / 2 - (box.y + box.height / 2) };
      }),
    );
    const worst = icons.reduce((a, b) => (Math.abs(b.off) > Math.abs(a.off) ? b : a), { name: "", off: 0 });
    check(`${scheme}: every icon is centred in its box vertically`, icons.length > 0 && Math.abs(worst.off) <= 0.8, `${icons.length} icons, worst ${worst.name} ${worst.off.toFixed(2)} units`);
    await ctx.close();
  }
} finally {
  await browser.close();
}
if (fails.length) {
  console.error(`\n${fails.length} failed: ${fails.join("; ")}`);
  process.exit(1);
}
