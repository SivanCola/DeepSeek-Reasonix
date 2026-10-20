import { chromium } from "playwright";

const BASE = process.env.PERF_URL ?? "http://localhost:4399/perf.html";
const fails = [];
const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, reducedMotion: "reduce" });
  for (const locale of ["zh", "en"]) {
    for (const git of ["none", "repo"]) {
      await page.goto(`${BASE}?pref=${locale}&turns=2&git=${git}`, { waitUntil: "networkidle" });
      await page.waitForSelector(".studio-branch .lb");
      await page.evaluate(() => document.fonts.ready);
      for (const scale of [1, 1.35]) {
        await page.evaluate((value) => document.documentElement.style.setProperty("--fs-scale", String(value)), scale);
        await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        const geometry = await page.evaluate(() => {
          const label = document.querySelector(".studio-branch .lb").getBoundingClientRect();
          const peer = document.querySelector(".studio-mode-picker > span:not(.studio-sr-label)").getBoundingClientRect();
          return { top: label.top - peer.top, height: label.height - peer.height };
        });
        const name = `${locale}/${git}/text-scale=${scale}`;
        const aligned = Math.abs(geometry.top) <= 0.5 && Math.abs(geometry.height) <= 0.5;
        console.log(`${aligned ? "ok" : "FAIL"} ${name}: ${JSON.stringify(geometry)}`);
        if (!aligned) fails.push(name);
      }
      await page.focus(".studio-branch");
      if (git === "none") await page.waitForSelector('.studio-branch-card[role="tooltip"]', { state: "visible" });
      if (git === "repo") {
        await page.click(".studio-branch");
        await page.waitForSelector('.workspace-palette[role="dialog"]', { state: "visible" });
        await page.keyboard.press("Escape");
      }
    }
  }
} finally {
  await browser.close();
}
if (fails.length) {
  console.error(`Misaligned Git labels: ${fails.join(", ")}`);
  process.exit(1);
}
