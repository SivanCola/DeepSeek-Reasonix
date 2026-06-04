const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const htmlPath = path.resolve(__dirname, "drawer-overlay-comparison.html");
  const outputPath = path.resolve(__dirname, "drawer-overlay-comparison.png");
  const browser = await chromium.launch({
    headless: true,
    executablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  });
  const page = await browser.newPage({ viewport: { width: 1560, height: 980 }, deviceScaleFactor: 2 });
  await page.goto(`file://${htmlPath}`, { waitUntil: "networkidle" });
  await page.screenshot({ path: outputPath, fullPage: true });
  await browser.close();
  console.log(outputPath);
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
