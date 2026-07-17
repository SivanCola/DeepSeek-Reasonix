import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = (path) => readFile(new URL(path, import.meta.url), "utf8");

/* Extract the body of a `@media (max-width: Npx)` block (up to the next
   at-rule or EOF) so assertions stay scoped to small viewports. */
const mediaBlock = (css, px) => {
  const marker = `@media (max-width: ${px}px)`;
  const start = css.indexOf(marker);
  assert.notEqual(start, -1, `missing ${marker}`);
  const rest = css.slice(start + marker.length);
  const next = rest.search(/@media|@keyframes/);
  return next === -1 ? rest : rest.slice(0, next);
};

/* The header must fit a 390px viewport: brand logo + language switch +
   theme switch + install button. These contraction rules are what keep the
   nav from overflowing horizontally (body has overflow-x: hidden). */
test("≤640px: marketing nav contracts to fit 390px viewports", async () => {
  const css = await source("../styles/global.css");
  const block = mediaBlock(css, 640);
  assert.match(block, /\.nav-sign-in \{ display: none/);
  assert.match(block, /\.nav \.brand span \{ display: none/);
  assert.match(block, /\.theme-switch button \{ padding: 6px 9px/);
});

test("≤640px: community nav contracts to fit 390px viewports", async () => {
  const css = await source("../styles/community.css");
  const block = mediaBlock(css, 640);
  assert.match(block, /\.brand \.ctag \{ display: none/);
  assert.match(block, /\.theme-switch button \{ padding: 6px 9px/);
});
