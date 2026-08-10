// Run: tsx src/__tests__/transcript-logical-selection.test.ts

import { JSDOM } from "jsdom";
import {
  domPointToTranscriptOffset,
  domRangeForTranscriptOffsets,
  projectTranscriptSelectableDom,
  transcriptRowsAtLogicalPromotion,
} from "../lib/transcriptSelectionDom";
import {
  TranscriptSelectionStore,
  type TranscriptSelectableRow,
  type TranscriptSelectionPoint,
} from "../lib/transcriptSelectionStore";
import { userMessageSelectionText } from "../lib/transcriptSelectionText";
import { formatSelectedTextContext, formatSelectionLabels } from "../lib/selectedTextContext";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const point = (rowKey: string, textOffset: number): TranscriptSelectionPoint => ({
  rowKey,
  textOffset,
  affinity: "forward",
});

const row = (rowKey: string, text: string, revision = 1, pin?: () => () => void): TranscriptSelectableRow => ({
  rowKey,
  sourceText: text,
  contentRevision: revision,
  resolveText: async () => text,
  pin,
});

console.log("\nlogical transcript selection store");

{
  const store = new TranscriptSelectionStore();
  const rows = [row("a", "alpha"), row("b", "bravo"), row("c", "charlie")];
  const id = store.beginNative("tab-a");
  store.updateNativeRange(point("a", 2), point("c", 3));
  eq(store.promoteToLogical("tab-a", point("a", 2), point("c", 3), rows), id, "cross-row selection promotes in the source tab");
  store.settleLogical();
  eq(await store.resolveText(id), "pha\n\nbravo\n\ncha", "forward selection resolves partial endpoints and full middle rows");
  eq(store.getSnapshot().mode, "logical-settled", "pointer release settles logical selection");
}

{
  const store = new TranscriptSelectionStore();
  const rows = [row("a", "A😀B"), row("b", "second")];
  const id = store.beginNative("tab-r");
  store.promoteToLogical("tab-r", point("b", 4), point("a", 3), rows);
  eq(store.getSnapshot().direction, "backward", "reverse drag records direction");
  eq(await store.resolveText(id), "B\n\nseco", "reverse drag still copies document order using UTF-16 offsets");
}

{
  let pins = 0;
  const store = new TranscriptSelectionStore();
  const rows = [row("a", "one", 1, () => { pins += 1; return () => { pins -= 1; }; }), row("b", "two")];
  store.beginNative("tab-p");
  store.promoteToLogical("tab-p", point("a", 0), point("b", 3), rows);
  eq(pins, 1, "promotion pins active cached projections");
  ok(store.validateRows([row("a", "one more", 2), row("b", "two")]), "append-only content keeps the frozen selected prefix");
  eq(store.getSnapshot().mode, "logical-dragging", "append-only validation does not clear selection");
  ok(!store.validateRows([row("a", "replaced", 3), row("b", "two")]), "non-append replacement invalidates selected content");
  eq(store.getSnapshot().mode, "none", "invalid content clears the logical selection");
  eq(pins, 0, "selection cleanup releases projection pins");
}

{
  let resolve!: (text: string) => void;
  const deferred = new Promise<string>((done) => { resolve = done; });
  const store = new TranscriptSelectionStore();
  const deferredRow: TranscriptSelectableRow = {
    rowKey: "a",
    sourceText: "fallback",
    contentRevision: 1,
    resolveText: () => deferred,
  };
  const id = store.beginNative("tab-stale");
  store.promoteToLogical("tab-stale", point("a", 0), point("a", 8), [deferredRow]);
  const pending = store.resolveText(id);
  store.clear("tab-switch");
  resolve("resolved");
  eq(await pending, "", "late async projection cannot resolve a cleared snapshot");
  ok(!store.isCurrent(id, "tab-stale"), "cleared snapshot id cannot be reused by async consumers");
}

console.log("\nlogical transcript DOM adapter");

{
  const dom = new JSDOM(`<!doctype html><body>
    <div class="transcript__row" data-row-key="a">
      <div id="root" data-transcript-selectable="message">
        <p>Hello <strong>world</strong><br>next</p>
        <p><strong>bold</strong> <em>italic</em></p>
        <span class="katex" data-latex-source="x^2"><span aria-hidden="true">rendered</span></span>
        <button>Copy</button>
        <table><tbody><tr><td>A</td><td>1</td></tr><tr><td>B</td><td>2</td></tr></tbody></table>
      </div>
      <div data-transcript-selectable="reasoning">visible thought</div>
    </div>
  </body>`);
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Range = dom.window.Range;
  const root = document.getElementById("root") as HTMLElement;
  const projection = projectTranscriptSelectableDom(root);
  eq(projection.text, "Hello world\nnext\nbold italic\n$x^2$\nA\t1\nB\t2", "DOM projection filters controls and restores formula/table structure");
  const hello = root.querySelector("p")?.firstChild as Text;
  eq(domPointToTranscriptOffset(root, hello, 3), 3, "DOM text boundary maps to a UTF-16 projection offset");
  const range = domRangeForTranscriptOffsets(root, 6, 11);
  eq(range?.toString(), "world", "logical offsets map back to a DOM highlight range");
  const promotionRows = transcriptRowsAtLogicalPromotion([
    row("a", "answer"),
    { ...row("hidden-reasoning", "secret"), kind: "reasoning" },
    { ...row("a", "visible thought"), rowKey: "a", kind: "reasoning" },
  ], document);
  eq(
    promotionRows.map((entry) => `${entry.rowKey}:${entry.kind ?? "message"}`).join(","),
    "a:message,a:reasoning",
    "logical promotion excludes reasoning text whose collapsible body is absent",
  );
}

console.log("\nuser transcript selection projection");

{
  const selected = [{ id: "s1", text: "quoted context" }];
  const labels = formatSelectionLabels(selected);
  const submit = `question @/tmp/report.md\n\n${formatSelectedTextContext(selected)}`;
  eq(
    userMessageSelectionText(`question @/tmp/report.md ${labels}`, submit),
    "question\n\nquoted context",
    "user projection keeps body and selected context while filtering attachment/card metadata",
  );
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
