import assert from "node:assert/strict";
import {
  createRasterPdf,
  neutralizeExternalCssResources,
  numberedExportPath,
  planRasterSlices,
} from "../lib/sessionExportCore";

const css = neutralizeExternalCssResources(`
  @font-face { font-family: Test; src: url("fonts/test.woff2") format("woff2"); }
  .safe { color: red; background-image: url(https://example.com/image.png); }
`);
assert.equal(css.includes("url("), false);
assert.match(css, /\.safe \{ color: red;/);

assert.deepEqual(planRasterSlices(25_000, 8_000), [
  { offset: 0, height: 8_000 },
  { offset: 8_000, height: 8_000 },
  { offset: 16_000, height: 8_000 },
  { offset: 24_000, height: 1_000 },
]);
assert.deepEqual(planRasterSlices(Number.NaN, 0), [{ offset: 0, height: 1 }]);
assert.deepEqual(planRasterSlices(25_000, 8_000, [7_000, 14_200, 23_000]), [
  { offset: 0, height: 7_000 },
  { offset: 7_000, height: 7_200 },
  { offset: 14_200, height: 8_000 },
  { offset: 22_200, height: 2_800 },
]);

assert.equal(numberedExportPath("C:\\Exports\\chat.png", 0, 3), "C:\\Exports\\chat-1-of-3.png");
assert.equal(numberedExportPath("/tmp/chat.archive.png", 2, 3), "/tmp/chat.archive-3-of-3.png");
assert.equal(numberedExportPath("chat.png", 0, 1), "chat.png");

const pdf = createRasterPdf(
  [
    { bytes: new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), width: 100, height: 120 },
    { bytes: new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), width: 100, height: 80 },
  ],
  "Export test",
);
const pdfText = new TextDecoder("latin1").decode(pdf);
assert.equal(pdfText.startsWith("%PDF-1.4"), true);
assert.match(pdfText, /\/Count 2/);
assert.equal((pdfText.match(/\/Subtype \/Image/g) ?? []).length, 2);
assert.match(pdfText, /startxref\n\d+\n%%EOF/);

console.log("session export tests passed");
