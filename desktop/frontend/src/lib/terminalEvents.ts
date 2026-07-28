import { onTerminalExit, onTerminalOutput, type TerminalExitEvent, type TerminalOutputEvent } from "./bridge";

const MAX_PENDING_BYTES = 1024 * 1024;

type TerminalSink = (data: Uint8Array) => void;

const sinks = new Map<string, TerminalSink>();
const exitListeners = new Set<(event: TerminalExitEvent) => void>();
const pending = new Map<string, Uint8Array[]>();
const pendingBytes = new Map<string, number>();
let started = false;
let stopBridge: (() => void) | null = null;

function decodeBase64(value: string): Uint8Array {
  if (typeof atob !== "function") return new Uint8Array();
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function deliverOutput(event: TerminalOutputEvent): void {
  const bytes = decodeBase64(event.data);
  if (bytes.byteLength === 0) return;
  const sink = sinks.get(event.id);
  if (sink) {
    sink(bytes);
    return;
  }
  const queue = pending.get(event.id) ?? [];
  queue.push(bytes);
  let total = (pendingBytes.get(event.id) ?? 0) + bytes.byteLength;
  while (total > MAX_PENDING_BYTES && queue.length > 0) {
    total -= queue.shift()?.byteLength ?? 0;
  }
  pending.set(event.id, queue);
  pendingBytes.set(event.id, total);
}

function deliverExit(_event: TerminalExitEvent): void {
  exitListeners.forEach((listener) => listener(_event));
}

export function startTerminalEventBridge(): () => void {
  if (!started) {
    started = true;
    const stopOutput = onTerminalOutput(deliverOutput);
    const stopExit = onTerminalExit(deliverExit);
    stopBridge = () => {
      stopOutput();
      stopExit();
      started = false;
      stopBridge = null;
    };
  }
  return () => stopBridge?.();
}

export function registerTerminalSink(id: string, sink: TerminalSink): () => void {
  sinks.set(id, sink);
  const queued = pending.get(id) ?? [];
  pending.delete(id);
  pendingBytes.delete(id);
  queued.forEach((bytes) => sink(bytes));
  return () => {
    if (sinks.get(id) === sink) sinks.delete(id);
  };
}

export function registerTerminalExitListener(listener: (event: TerminalExitEvent) => void): () => void {
  exitListeners.add(listener);
  return () => exitListeners.delete(listener);
}

export function __resetTerminalEventBus(): void {
  sinks.clear();
  pending.clear();
  pendingBytes.clear();
  stopBridge?.();
}

export const terminalEventBufferLimit = MAX_PENDING_BYTES;
