import { JSDOM } from "jsdom";

import { __emitMockTerminalOutput } from "../lib/bridge";
import { __resetTerminalEventBus, registerTerminalSink, startTerminalEventBridge, terminalEventBufferLimit } from "../lib/terminalEvents";

function base64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}

function check(condition: unknown, message: string): void {
  if (!condition) throw new Error(message);
  process.stdout.write(`PASS ${message}\n`);
}

const dom = new JSDOM("<!doctype html><html><body></body></html>");
const previousWindow = globalThis.window;
const previousAtob = globalThis.atob;
const previousBtoa = globalThis.btoa;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.atob = (value: string) => Buffer.from(value, "base64").toString("binary");
globalThis.btoa = (value: string) => Buffer.from(value, "binary").toString("base64");

try {
  __resetTerminalEventBus();
  startTerminalEventBridge();
  const first: number[] = [];
  __emitMockTerminalOutput({ id: "utf8", data: base64(new Uint8Array([0xe4, 0xbd])) });
  __emitMockTerminalOutput({ id: "utf8", data: base64(new Uint8Array([0xa0])) });
  const unregister = registerTerminalSink("utf8", (bytes) => first.push(...bytes));
  unregister();
  check(JSON.stringify(first) === JSON.stringify([0xe4, 0xbd, 0xa0]), "split UTF-8 output is delivered as original bytes");

  const chunk = new Uint8Array(64 * 1024);
  chunk.fill(65);
  for (let index = 0; index < 24; index += 1) {
    __emitMockTerminalOutput({ id: "bounded", data: base64(chunk) });
  }
  const bounded: number[] = [];
  const unregisterBounded = registerTerminalSink("bounded", (bytes) => bounded.push(...bytes));
  unregisterBounded();
  check(bounded.length <= terminalEventBufferLimit, "pending output is bounded to one MiB per session");
} finally {
  __resetTerminalEventBus();
  if (previousWindow) globalThis.window = previousWindow;
  else delete (globalThis as { window?: unknown }).window;
  if (previousAtob) globalThis.atob = previousAtob;
  if (previousBtoa) globalThis.btoa = previousBtoa;
}
