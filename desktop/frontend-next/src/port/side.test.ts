import { afterEach, describe, expect, it, vi } from "vitest";
import { SseExtensions } from "./sse_ext";
import { HttpError } from "./http_error";
import type { SideProgress } from "./side";

const encoder = new TextEncoder();
const frame = (event: string, data: unknown) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;

afterEach(() => vi.unstubAllGlobals());

function connect() {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(value) { controller = value; } });
  vi.stubGlobal("fetch", async () => new Response(body, { headers: { "content-type": "text/event-stream" } }));
  const progress: SideProgress[] = [];
  const answer = new SseExtensions("/rt/r1").askSide("side-1", "question", [], (value) => progress.push(value));
  return { controller, progress, answer, send: (event: string, data: unknown) => controller.enqueue(encoder.encode(frame(event, data))) };
}

describe("private side answer streams", () => {
  it("delivers partial output before completion and uses the final answer without duplicating deltas", async () => {
    const stream = connect();
    let completed = false;
    void stream.answer.then(() => { completed = true; });
    stream.send("progress", { kind: "thinking" });
    stream.send("progress", { kind: "text", text: "An interim observation" });
    await vi.waitFor(() => expect(stream.progress).toEqual([
      { kind: "thinking" }, { kind: "text", text: "An interim observation" },
    ]));
    expect(completed).toBe(false);
    stream.send("done", { answer: "The final conclusion" });
    expect(await stream.answer).toBe("The final conclusion");
  });

  it("decodes Unicode and SSE frames split at every byte, including CRLF separators", async () => {
    const stream = connect();
    const bytes = encoder.encode((frame("progress", { kind: "text", text: "你好\n世界" }) + frame("done", { answer: "答案" })).replaceAll("\n", "\r\n"));
    for (const byte of bytes) stream.controller.enqueue(new Uint8Array([byte]));
    stream.controller.close();
    expect(await stream.answer).toBe("答案");
    expect(stream.progress).toEqual([{ kind: "text", text: "你好\n世界" }]);
  });

  it("preserves a typed failure that arrives after partial output", async () => {
    const stream = connect();
    const failure = stream.answer.catch((error: unknown) => error);
    stream.send("progress", { kind: "text", text: "Partial evidence" });
    stream.send("error", { status: 409, code: "side.busy", error: "still answering" });
    const error = await failure;
    expect(error).toBeInstanceOf(HttpError);
    expect((error as HttpError).status).toBe(409);
    expect((error as HttpError).reason?.code).toBe("side.busy");
    expect(stream.progress).toEqual([{ kind: "text", text: "Partial evidence" }]);
  });

  it("never presents a truncated stream as a completed answer", async () => {
    const stream = connect();
    const failure = stream.answer.catch((error: unknown) => error);
    stream.send("progress", { kind: "text", text: "Not a complete answer" });
    stream.controller.close();
    expect(await failure).toBeInstanceOf(HttpError);
    expect(((await failure) as HttpError).reason?.code).toBe("side.failed");
    expect(stream.progress).toEqual([{ kind: "text", text: "Not a complete answer" }]);
  });
});
