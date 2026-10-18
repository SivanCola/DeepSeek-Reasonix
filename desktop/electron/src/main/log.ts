import { closeSync, mkdirSync, openSync, renameSync, statSync, writeSync } from "node:fs";
import { dirname } from "node:path";
import type { Writable } from "node:stream";

export const LOG_ROTATE_BYTES = 5 * 1024 * 1024;

export interface Logger {
  info(message: string): void;
  warn(message: string): void;
  error(message: string): void;
}

export class RotatingFile {
  private fd: number | null = null;
  private size = 0;

  constructor(readonly path: string, private readonly limit = LOG_ROTATE_BYTES) {}

  write(chunk: Buffer | string): void {
    const data = typeof chunk === "string" ? Buffer.from(chunk) : chunk;
    try {
      if (this.fd === null) this.open();
      if (this.size + data.length > this.limit) this.rotate();
      writeSync(this.fd as number, data);
      this.size += data.length;
    } catch {
      // Logging must never take the shell down.
    }
  }

  close(): void {
    if (this.fd === null) return;
    try {
      closeSync(this.fd);
    } catch {
      // Nothing to recover.
    }
    this.fd = null;
  }

  private open(): void {
    mkdirSync(dirname(this.path), { recursive: true });
    this.fd = openSync(this.path, "a");
    this.size = statSync(this.path).size;
  }

  private rotate(): void {
    this.close();
    renameSync(this.path, `${this.path}.1`);
    this.open();
  }
}

export function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  try {
    return JSON.stringify(error);
  } catch {
    return String(error);
  }
}

// Share one guarded sink between shell diagnostics and service stderr. Both
// asynchronous EPIPE and synchronous EIO must stop echoing before an exception
// handler logs again. Backpressure must not create an unbounded output queue.
export function createOutputEcho(stream: Writable = process.stderr): (chunk: Buffer | string) => void {
  let failed = false;
  let blocked = false;
  stream.on("error", () => { failed = true; });
  stream.on("drain", () => { blocked = false; });
  return (chunk) => {
    if (failed || blocked || stream.destroyed || stream.writableEnded) return;
    try {
      blocked = !stream.write(chunk, (error) => { if (error) failed = true; });
    } catch {
      failed = true;
    }
  };
}

export function createLogger(file: RotatingFile, echo: boolean | ((chunk: Buffer | string) => void)): Logger {
  const writeEcho = typeof echo === "function" ? echo : echo ? createOutputEcho() : undefined;
  const emit = (level: string, message: string) => {
    const line = `${new Date().toISOString()} ${level} ${message}\n`;
    file.write(line);
    writeEcho?.(`[shell] ${line}`);
  };
  return {
    info: (message) => emit("info", message),
    warn: (message) => emit("warn", message),
    error: (message) => emit("error", message),
  };
}
