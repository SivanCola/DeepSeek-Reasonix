import assert from "node:assert/strict";
import { chromium } from "playwright";

const base = process.env.PERF_URL ?? "http://localhost:4399/perf.html?pref=zh&turns=4";
const browser = await chromium.launch({ ignoreDefaultArgs: ["--hide-scrollbars"] });
const box = 'textarea[role="combobox"]';
const text = "planning这个skill刚刚更新完，并且跑了一次情报规划任务，现在需要对collection技能做同步优化。";
try {
  for (const [zoom, scrollbar] of [[1, false], [0.85, false], [1.5, false], [1, true]]) {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: scrollbar ? "light" : "dark" });
    page.setDefaultTimeout(8000);
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const url = new URL(base);
    url.searchParams.set("zoom", String(zoom));
    await page.goto(url.href, { waitUntil: "networkidle" });
    await page.evaluate(() => document.fonts.ready);
    if (scrollbar) {
      await page.addStyleTag({ content: ".compose textarea { overflow-y: scroll; scrollbar-gutter: stable; scrollbar-width: auto; } .compose textarea::-webkit-scrollbar { width: 14px; }" });
      if (await page.locator(box).evaluate((el) => el.offsetWidth === el.clientWidth)) {
        console.log("SKIP native scrollbar gutter: this browser uses overlay scrollbars");
        await page.close();
        continue;
      }
    }
    await page.locator(box).fill("/init");
    await page.locator(".slashmenu").waitFor();
    await page.locator(box).press("Enter");
    await page.locator(".skillchip").waitFor();
    await page.locator(box).pressSequentially(text.repeat(4));
    for (const width of [1440, 960, 640, 420, 1440]) {
      await page.evaluate(() => {
        window.__reflowFrames = [];
        const sample = () => {
          if (!window.__reflowFrames) return;
          const input = document.querySelector('textarea[role="combobox"]');
          const mirror = document.querySelector(".chipmirror");
          const compose = document.querySelector(".compose").getBoundingClientRect();
          const range = document.createRange();
          range.selectNodeContents(mirror);
          window.__reflowFrames.push({
            text: input.value,
            caret: input.selectionStart,
            scrollbar: input.offsetWidth - input.clientWidth,
            inputWidth: input.getBoundingClientRect().width,
            mirrorWidth: mirror.getBoundingClientRect().width,
            inputScroll: input.scrollHeight,
            mirrorScroll: mirror.scrollHeight,
            outside: [...range.getClientRects()].filter((r) => r.right > compose.right + 1 || r.left < compose.left - 1).length,
          });
          requestAnimationFrame(sample);
        };
        requestAnimationFrame(sample);
      });
      await page.setViewportSize({ width, height: 900 });
      const states = await page.evaluate(async () => {
        for (let frame = 0; frame < 15; frame++) await new Promise(requestAnimationFrame);
        const states = window.__reflowFrames;
        window.__reflowFrames = null;
        return states;
      });
      for (const state of states) {
        assert.equal(state.text, "/init " + text.repeat(4));
        assert.equal(state.caret, state.text.length);
        assert.ok(Math.abs(state.inputWidth - state.mirrorWidth) <= 1, `mirror follows the resized input: ${JSON.stringify(state)}`);
        assert.ok(Math.abs(state.inputScroll - state.mirrorScroll) <= 1, `mirror and caret wrap on the same lines: ${JSON.stringify(state)}`);
        assert.equal(state.outside, 0, "draft glyphs stay inside the composer");
        if (scrollbar) assert.ok(state.scrollbar >= 14, "exercise a space-consuming native scrollbar");
      }
      console.log(`PASS zoom=${zoom} width=${width} scrollbar=${scrollbar}: draft, caret, wrapping and containment through the transition`);
    }
    assert.deepEqual(errors, []);
    await page.close();
  }
} finally {
  await browser.close();
}
