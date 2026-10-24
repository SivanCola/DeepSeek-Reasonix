import assert from "node:assert/strict";
import { chromium } from "playwright";

const base = process.env.PERF_URL ?? "http://localhost:4399/perf.html?pref=zh&turns=0";
const browser = await chromium.launch();
try {
  for (const scheme of ["light", "dark"]) {
    const page = await browser.newPage({ viewport: { width: 1600, height: 860 }, colorScheme: scheme });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(base, { waitUntil: "networkidle" });
    await page.waitForSelector(".flowgrip-l");
    const paint = (selector) => page.locator(selector).evaluate((el) => getComputedStyle(el, "::before").backgroundColor);
    const alpha = (css) => (css.startsWith("rgba") ? Number(css.split(",")[3].replace(")", "")) : css === "transparent" ? 0 : 1);
    await page.mouse.move(800, 400);
    for (const side of ["l", "r"]) {
      assert.equal(alpha(await paint(`.flowgrip-${side}`)), 0, `${scheme}: the ${side} grip draws nothing at rest`);
    }
    const box = await page.locator(".flowgrip-l").boundingBox();
    await page.mouse.move(box.x + 3, box.y + box.height / 2);
    await page.waitForTimeout(400);
    assert.ok(alpha(await paint(".flowgrip-l")) > 0, `${scheme}: the grip appears under the pointer`);
    const before = await page.evaluate(() => Math.round(document.querySelector(".compose").getBoundingClientRect().width));
    await page.mouse.down();
    await page.mouse.move(box.x - 60, box.y + box.height / 2, { steps: 6 });
    await page.mouse.up();
    await page.waitForTimeout(200);
    const after = await page.evaluate(() => Math.round(document.querySelector(".compose").getBoundingClientRect().width));
    assert.ok(after > before, `${scheme}: a hidden grip still resizes the column (${before} -> ${after})`);
    await page.mouse.move(800, 400);
    await page.waitForTimeout(400);
    assert.equal(alpha(await paint(".flowgrip-l")), 0, `${scheme}: it hides again once the pointer leaves`);
    assert.deepEqual(errors, []);
    console.log(`PASS ${scheme}: grips are invisible at rest, appear under the pointer, and still resize`);
    await page.close();
  }
} finally {
  await browser.close();
}
